package controller

import (
	"context"
	"errors"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

const (
	defaultActivePollInterval = 2500 * time.Millisecond
	defaultSubscriberGrace    = 15 * time.Second
	defaultOperationTimeout   = 10 * time.Second
)

type STMClient interface {
	SendTelegram(ctx context.Context, stmIndex, moduleAddress, content int) ([]int, error)
	SimInputEvent(ctx context.Context, stmIndex, module, channel, eventType, keyType int) error
}

type Config struct {
	ActivePollInterval time.Duration
	SubscriberGrace    time.Duration
	OperationTimeout   time.Duration
	// IdleHealthInterval, when > 0, probes one module on this interval while no
	// browser is connected, so connection status stays fresh for health checks.
	// Zero (the default) disables it, keeping the STM idle when nobody is watching.
	IdleHealthInterval time.Duration
	QueueDepth         int
	EventBuffer        int
	DisableRefresh     bool
}

func (c Config) normalized() (Config, error) {
	if c.ActivePollInterval < 0 || c.SubscriberGrace < 0 || c.OperationTimeout < 0 ||
		c.IdleHealthInterval < 0 || c.QueueDepth < 0 || c.EventBuffer < 0 {
		return Config{}, errors.New("controller: durations and capacities cannot be negative")
	}
	if c.ActivePollInterval == 0 {
		c.ActivePollInterval = defaultActivePollInterval
	}
	if c.SubscriberGrace == 0 {
		c.SubscriberGrace = defaultSubscriberGrace
	}
	if c.OperationTimeout == 0 {
		c.OperationTimeout = defaultOperationTimeout
	}
	if c.QueueDepth == 0 {
		c.QueueDepth = 64
	}
	if c.EventBuffer == 0 {
		c.EventBuffer = 32
	}
	return c, nil
}

type Action string

const (
	ActionOn         Action = "on"
	ActionOff        Action = "off"
	ActionToggle     Action = "toggle"
	ActionRaise      Action = "raise"
	ActionLower      Action = "lower"
	ActionStop       Action = "stop"
	ActionTiltOpen   Action = "tiltOpen"
	ActionTiltClose  Action = "tiltClose"
	ActionShortPress Action = "shortPress"
	ActionLongPress  Action = "longPress"
	ActionActivate   Action = "activate"
)

type Capability struct {
	Action       Action `json:"action"`
	Experimental bool   `json:"experimental,omitempty"`
}

type CommandResult struct {
	ID string `json:"id"`
}

var (
	ErrControllerStopped = errors.New("controller: stopped")
	ErrDeviceNotFound    = errors.New("controller: device not found")
	ErrUnsupportedAction = errors.New("controller: action unsupported by device")
)

type PowerState string

const (
	PowerUnknown PowerState = "unknown"
	PowerOff     PowerState = "off"
	PowerOn      PowerState = "on"
)

type DeviceState struct {
	Power PowerState `json:"power"`
}

type ConnectionStatus string

const (
	ConnectionConnected    ConnectionStatus = "connected"
	ConnectionDisconnected ConnectionStatus = "disconnected"
)

type Snapshot struct {
	Revision   uint64                 `json:"revision"`
	Connection ConnectionStatus       `json:"connection"`
	Stale      bool                   `json:"stale"`
	Devices    map[string]DeviceState `json:"devices"`
}

type EventKind string

const (
	EventSnapshot   EventKind = "snapshot"
	EventState      EventKind = "state"
	EventConnection EventKind = "connection"
)

type Event struct {
	Kind       EventKind        `json:"kind"`
	Revision   uint64           `json:"revision"`
	Snapshot   *Snapshot        `json:"snapshot,omitempty"`
	DeviceID   string           `json:"deviceID,omitempty"`
	State      *DeviceState     `json:"state,omitempty"`
	Connection ConnectionStatus `json:"connection,omitempty"`
	Stale      bool             `json:"stale"`
}

type polledChannel struct {
	deviceID string
	channel  int
}

func amdBusAddress(ref domain.ChannelRef) (int, error) {
	if ref.ModuleClass != domain.ModuleAMD || ref.DIP < 0 || ref.DIP > 0x3f ||
		ref.Channel < 0 || ref.Channel > 63 {
		return 0, errors.New("controller: invalid AMD channel reference")
	}
	return 0x40 | ref.DIP, nil
}
