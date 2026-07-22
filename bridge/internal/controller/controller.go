package controller

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type subscriber struct {
	channel chan Event
	ready   bool
}

type Controller struct {
	project domain.Project
	client  STMClient
	config  Config

	devices         map[string]domain.Device
	modules         map[int][]polledChannel
	moduleAddresses []int
	deviceModules   map[string]int
	scheduler       *scheduler

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	wg        sync.WaitGroup
	stopped   atomic.Bool
	commandID atomic.Uint64

	stateMu    sync.Mutex
	revision   uint64
	connection ConnectionStatus
	stale      bool
	states     map[string]DeviceState

	subsMu          sync.Mutex
	subscribers     map[uint64]*subscriber
	nextSubscriber  uint64
	subscriberEpoch uint64
	pollWake        chan struct{}
	refreshModule   chan int
}

func New(project domain.Project, client STMClient, config Config) (*Controller, error) {
	if client == nil {
		return nil, fmt.Errorf("controller: nil STM client")
	}
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		project:       project,
		client:        client,
		config:        config,
		devices:       make(map[string]domain.Device),
		modules:       make(map[int][]polledChannel),
		deviceModules: make(map[string]int),
		scheduler:     newScheduler(config.QueueDepth),
		ctx:           ctx,
		cancel:        cancel,
		connection:    ConnectionDisconnected,
		stale:         true,
		states:        make(map[string]DeviceState),
		subscribers:   make(map[uint64]*subscriber),
		pollWake:      make(chan struct{}, 1),
		refreshModule: make(chan int, config.QueueDepth),
	}

	for _, floor := range project.Floors {
		for _, device := range floor.Devices {
			if device.ID == "" {
				c.cleanupConstruction()
				return nil, fmt.Errorf("controller: device without stable ID")
			}
			if _, duplicate := c.devices[device.ID]; duplicate {
				c.cleanupConstruction()
				return nil, fmt.Errorf("controller: duplicate device ID %q", device.ID)
			}
			c.devices[device.ID] = device
			if device.Ref.ModuleClass != domain.ModuleAMD {
				continue
			}
			busAddress, err := amdBusAddress(device.Ref)
			if err != nil {
				c.cleanupConstruction()
				return nil, fmt.Errorf("controller: device %q: %w", device.ID, err)
			}
			c.modules[busAddress] = append(c.modules[busAddress], polledChannel{
				deviceID: device.ID,
				channel:  device.Ref.Channel,
			})
			c.deviceModules[device.ID] = busAddress
			c.states[device.ID] = DeviceState{Power: PowerUnknown}
		}
	}
	for address := range c.modules {
		c.moduleAddresses = append(c.moduleAddresses, address)
	}
	sort.Ints(c.moduleAddresses)

	c.wg.Add(1)
	go c.pollLoop()
	return c, nil
}

func (c *Controller) cleanupConstruction() {
	c.cancel()
	c.scheduler.close()
}

func (c *Controller) Project() domain.Project {
	return c.project
}

func (c *Controller) Device(deviceID string) (domain.Device, bool) {
	device, ok := c.devices[deviceID]
	return device, ok
}

func (c *Controller) Snapshot() Snapshot {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	states := make(map[string]DeviceState, len(c.states))
	for id, state := range c.states {
		states[id] = state
	}
	return Snapshot{
		Revision:   c.revision,
		Connection: c.connection,
		Stale:      c.stale,
		Devices:    states,
	}
}

// RunBackground schedules one bounded, non-command STM operation. Project
// reload uses this once per readFile chunk, allowing queued user commands to
// run between chunks without racing polling on the STM connection.
func (c *Controller) RunBackground(
	ctx context.Context,
	operation func(context.Context) (any, error),
) (any, error) {
	if c.stopped.Load() {
		return nil, ErrControllerStopped
	}
	if operation == nil {
		return nil, fmt.Errorf("controller: nil background operation")
	}
	return c.scheduler.poll(ctx, operation)
}

// Subscribe returns a best-effort revisioned event stream. A slow consumer may
// miss deltas, detect the revision gap, and request a fresh Snapshot.
func (c *Controller) Subscribe(buffer int) (<-chan Event, func(), error) {
	if c.stopped.Load() {
		return nil, nil, ErrControllerStopped
	}
	if buffer < 1 {
		buffer = c.config.EventBuffer
	}
	channel := make(chan Event, buffer)

	c.subsMu.Lock()
	if c.stopped.Load() {
		c.subsMu.Unlock()
		close(channel)
		return nil, nil, ErrControllerStopped
	}
	first := len(c.subscribers) == 0
	c.nextSubscriber++
	id := c.nextSubscriber
	c.subscribers[id] = &subscriber{channel: channel}
	if first {
		c.subscriberEpoch++
	}
	c.subsMu.Unlock()

	if first {
		c.setStale(true)
	}
	snapshot := c.Snapshot()
	c.subsMu.Lock()
	if entry := c.subscribers[id]; entry != nil {
		entry.ready = true
		entry.channel <- Event{Kind: EventSnapshot, Revision: snapshot.Revision, Snapshot: &snapshot}
	}
	c.subsMu.Unlock()
	c.wakePoller()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			c.subsMu.Lock()
			entry := c.subscribers[id]
			delete(c.subscribers, id)
			if entry != nil {
				close(entry.channel)
			}
			c.subsMu.Unlock()
			c.wakePoller()
		})
	}
	return channel, cancel, nil
}

func (c *Controller) publish(event Event) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, subscriber := range c.subscribers {
		if !subscriber.ready {
			continue
		}
		select {
		case subscriber.channel <- event:
		default:
		}
	}
}

func (c *Controller) subscriberState() (count int, epoch uint64) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	return len(c.subscribers), c.subscriberEpoch
}

func (c *Controller) wakePoller() {
	select {
	case c.pollWake <- struct{}{}:
	default:
	}
}

func (c *Controller) Close() {
	c.closeOnce.Do(func() {
		c.stopped.Store(true)
		c.cancel()
		c.scheduler.close()
		c.wg.Wait()
		c.subsMu.Lock()
		for id, subscriber := range c.subscribers {
			close(subscriber.channel)
			delete(c.subscribers, id)
		}
		c.subsMu.Unlock()
	})
}
