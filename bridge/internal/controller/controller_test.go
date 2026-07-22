package controller

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type recordedCall struct {
	method string
	values []int
}

type recordingSTM struct {
	mu        sync.Mutex
	calls     []recordedCall
	bitmasks  map[int]int
	sendError error
}

type gatedSTM struct {
	*recordingSTM
	firstInputStarted chan struct{}
	releaseFirstInput chan struct{}
	once              sync.Once
}

func (s *gatedSTM) SimInputEvent(
	ctx context.Context,
	stmIndex, module, channel, eventType, keyType int,
) error {
	if err := s.recordingSTM.SimInputEvent(ctx, stmIndex, module, channel, eventType, keyType); err != nil {
		return err
	}
	blocked := false
	s.once.Do(func() {
		blocked = true
		close(s.firstInputStarted)
	})
	if blocked {
		select {
		case <-s.releaseFirstInput:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *recordingSTM) SendTelegram(
	_ context.Context,
	stmIndex, moduleAddress, content int,
) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, recordedCall{method: "telegram", values: []int{stmIndex, moduleAddress, content}})
	if s.sendError != nil {
		return nil, s.sendError
	}
	return []int{0, moduleAddress, 0, 0, s.bitmasks[moduleAddress]}, nil
}

func (s *recordingSTM) SimInputEvent(
	_ context.Context,
	stmIndex, module, channel, eventType, keyType int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, recordedCall{
		method: "input",
		values: []int{stmIndex, module, channel, eventType, keyType},
	})
	return s.sendError
}

func (s *recordingSTM) snapshotCalls() []recordedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]recordedCall, len(s.calls))
	copy(result, s.calls)
	return result
}

func (s *recordingSTM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func testProject() domain.Project {
	down := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 4, Channel: 0}
	up := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 4, Channel: 1}
	jalDown := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 4, Channel: 2}
	jalUp := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 4, Channel: 3}
	devices := []domain.Device{
		{ID: "light", Name: "Light", Kind: domain.KindLight, Ref: domain.ChannelRef{ModuleClass: domain.ModuleAMD, DIP: 1, Channel: 2}},
		{ID: "outlet", Name: "Outlet", Kind: domain.KindOutlet, Ref: domain.ChannelRef{ModuleClass: domain.ModuleAMD, DIP: 1, Channel: 5}},
		{ID: "other-light", Name: "Other", Kind: domain.KindLight, Ref: domain.ChannelRef{ModuleClass: domain.ModuleAMD, DIP: 2, Channel: 1}},
		{ID: "shutter", Name: "Blind", Kind: domain.KindShutter, Category: "Rollo", Ref: down, UpRef: &up},
		{ID: "jalousie", Name: "Terrace", Kind: domain.KindShutter, Category: "Jalousie", Ref: jalDown, UpRef: &jalUp},
		{ID: "scene", Name: "All off", Kind: domain.KindScene, Ref: domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 7, Channel: 0}},
		{ID: "button", Name: "Unknown", Kind: domain.KindButton, Ref: domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 8, Channel: 6}},
	}
	return domain.Project{
		Name:   "Test",
		Floors: []domain.Floor{{ID: "floor", Name: "Floor", Devices: devices}},
	}
}

func TestExecuteUsesExactHardwareCommands(t *testing.T) {
	tests := []struct {
		name     string
		deviceID string
		action   Action
		calls    []recordedCall
	}{
		{"light on", "light", ActionOn, []recordedCall{{"telegram", []int{0, 0x41, (2 << 5) | 2}}}},
		{"outlet off", "outlet", ActionOff, []recordedCall{{"telegram", []int{0, 0x41, (5 << 5) | 3}}}},
		{"light toggle", "light", ActionToggle, []recordedCall{{"telegram", []int{0, 0x41, (2 << 5) | 6}}}},
		{"shutter lower", "shutter", ActionLower, inputCalls(4, 0, 2, 4, 5)},
		{"shutter raise", "shutter", ActionRaise, inputCalls(4, 1, 2, 4, 5)},
		{"shutter stop", "shutter", ActionStop, inputCalls(4, 0, 2, 3)},
		{"jalousie tilt open", "jalousie", ActionTiltOpen, inputCalls(4, 3, 2, 4)},
		{"jalousie tilt close", "jalousie", ActionTiltClose, inputCalls(4, 2, 2, 4)},
		{"scene", "scene", ActionActivate, inputCalls(7, 0, 2, 4, 5)},
		{"button short", "button", ActionShortPress, inputCalls(8, 6, 2, 4, 5)},
		{"button long", "button", ActionLongPress, inputCalls(8, 6, 2, 3)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stm := &recordingSTM{bitmasks: make(map[int]int)}
			controller, err := New(testProject(), stm, Config{DisableRefresh: true})
			if err != nil {
				t.Fatal(err)
			}
			defer controller.Close()
			result, err := controller.Execute(context.Background(), test.deviceID, test.action)
			if err != nil {
				t.Fatal(err)
			}
			if result.ID == "" {
				t.Fatal("missing command ID")
			}
			if got := stm.snapshotCalls(); !reflect.DeepEqual(got, test.calls) {
				t.Fatalf("calls = %#v, want %#v", got, test.calls)
			}
		})
	}
}

func inputCalls(module, channel int, events ...int) []recordedCall {
	result := make([]recordedCall, 0, len(events))
	for _, event := range events {
		result = append(result, recordedCall{"input", []int{0, module, channel, event, 4}})
	}
	return result
}

func TestCapabilitiesRejectUnsupportedActions(t *testing.T) {
	controller, err := New(testProject(), &recordingSTM{}, Config{DisableRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	capabilities, err := controller.Capabilities("jalousie")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capabilities, []Capability{
		{Action: ActionRaise}, {Action: ActionStop}, {Action: ActionLower},
		{Action: ActionTiltOpen, Experimental: true},
		{Action: ActionTiltClose, Experimental: true},
	}) {
		t.Fatalf("jalousie capabilities = %#v", capabilities)
	}
	if _, err := controller.Execute(context.Background(), "shutter", ActionTiltOpen); !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("plain shutter tilt error = %v", err)
	}
	if _, err := controller.Execute(context.Background(), "missing", ActionOn); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("missing device error = %v", err)
	}
}

func TestInputSequenceIsAtomicAgainstPolling(t *testing.T) {
	stm := &gatedSTM{
		recordingSTM:      &recordingSTM{bitmasks: map[int]int{0x41: 0}},
		firstInputStarted: make(chan struct{}),
		releaseFirstInput: make(chan struct{}),
	}
	controller, err := New(testProject(), stm, Config{DisableRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	commandDone := make(chan error, 1)
	go func() {
		_, err := controller.Execute(context.Background(), "shutter", ActionLower)
		commandDone <- err
	}()
	<-stm.firstInputStarted
	pollDone := make(chan error, 1)
	go func() { pollDone <- controller.pollModule(0x41) }()
	waitFor(t, time.Second, func() bool { return len(controller.scheduler.background) == 1 })
	close(stm.releaseFirstInput)
	if err := <-commandDone; err != nil {
		t.Fatal(err)
	}
	if err := <-pollDone; err != nil {
		t.Fatal(err)
	}

	want := append(inputCalls(4, 0, 2, 4, 5), recordedCall{"telegram", []int{0, 0x41, 1}})
	if got := stm.snapshotCalls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want atomic input sequence before poll %#v", got, want)
	}
}

func TestPollingIsSubscriberAwareAndDecodesChangedState(t *testing.T) {
	stm := &recordingSTM{bitmasks: map[int]int{
		0x41: 1 << 2,
		0x42: 1 << 1,
	}}
	controller, err := New(testProject(), stm, Config{
		ActivePollInterval: 20 * time.Millisecond,
		SubscriberGrace:    60 * time.Millisecond,
		OperationTimeout:   time.Second,
		EventBuffer:        64,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	time.Sleep(30 * time.Millisecond)
	if got := stm.callCount(); got != 0 {
		t.Fatalf("polled with no subscriber: %d calls", got)
	}

	events, unsubscribe, err := controller.Subscribe(64)
	if err != nil {
		t.Fatal(err)
	}
	first := <-events
	if first.Kind != EventSnapshot || first.Snapshot == nil || !first.Snapshot.Stale {
		t.Fatalf("first event = %#v", first)
	}
	waitFor(t, time.Second, func() bool {
		snapshot := controller.Snapshot()
		return !snapshot.Stale && snapshot.Connection == ConnectionConnected &&
			snapshot.Devices["light"].Power == PowerOn &&
			snapshot.Devices["outlet"].Power == PowerOff &&
			snapshot.Devices["other-light"].Power == PowerOn
	})
	waitFor(t, time.Second, func() bool { return stm.callCount() >= 4 })

	stateEvents := 0
	drain := true
	for drain {
		select {
		case event := <-events:
			if event.Kind == EventState {
				stateEvents++
			}
		default:
			drain = false
		}
	}
	if stateEvents != 3 {
		t.Fatalf("state events after unchanged repeated sweep = %d, want 3", stateEvents)
	}

	unsubscribe()
	countAtUnsubscribe := stm.callCount()
	waitFor(t, time.Second, func() bool { return stm.callCount() > countAtUnsubscribe })
	waitFor(t, time.Second, func() bool {
		before := stm.callCount()
		time.Sleep(50 * time.Millisecond)
		return stm.callCount() == before
	})
}

func TestRapidReconnectTriggersImmediateFreshSweep(t *testing.T) {
	stm := &recordingSTM{bitmasks: map[int]int{0x41: 0, 0x42: 0}}
	controller, err := New(testProject(), stm, Config{
		ActivePollInterval: time.Second,
		SubscriberGrace:    200 * time.Millisecond,
		OperationTimeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	_, unsubscribe, err := controller.Subscribe(8)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return stm.callCount() == 2 })
	unsubscribe()
	before := stm.callCount()
	time.Sleep(20 * time.Millisecond)
	_, unsubscribeAgain, err := controller.Subscribe(8)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribeAgain()
	waitFor(t, time.Second, func() bool { return stm.callCount() >= before+2 })
}

func TestPowerCommandRefreshesOnceWithoutStartingContinuousPolling(t *testing.T) {
	stm := &recordingSTM{bitmasks: map[int]int{0x41: 1 << 2}}
	controller, err := New(testProject(), stm, Config{ActivePollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	if _, err := controller.Execute(context.Background(), "light", ActionOn); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return stm.callCount() == 2 })
	time.Sleep(40 * time.Millisecond)
	if got := stm.callCount(); got != 2 {
		t.Fatalf("post-command refresh started continuous polling: %d calls", got)
	}
}

func TestIdleHealthProbesWithoutSubscribers(t *testing.T) {
	stm := &recordingSTM{}
	controller, err := New(testProject(), stm, Config{
		DisableRefresh:     true,
		IdleHealthInterval: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	// No Subscribe at all: the idle health probe alone must reach the STM.
	waitFor(t, time.Second, func() bool { return stm.callCount() >= 2 })
}

func TestIdleHealthDisabledByDefault(t *testing.T) {
	stm := &recordingSTM{}
	controller, err := New(testProject(), stm, Config{DisableRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	time.Sleep(60 * time.Millisecond)
	if got := stm.callCount(); got != 0 {
		t.Errorf("idle health off + no subscribers: want 0 STM calls, got %d", got)
	}
}
