package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

func (c *Controller) Capabilities(deviceID string) ([]Capability, error) {
	device, ok := c.devices[deviceID]
	if !ok {
		return nil, ErrDeviceNotFound
	}
	return capabilitiesFor(device), nil
}

func capabilitiesFor(device domain.Device) []Capability {
	switch device.Kind {
	case domain.KindLight, domain.KindOutlet:
		return []Capability{{Action: ActionOn}, {Action: ActionOff}, {Action: ActionToggle}}
	case domain.KindShutter:
		capabilities := []Capability{{Action: ActionRaise}, {Action: ActionStop}, {Action: ActionLower}}
		if isJalousie(device) {
			capabilities = append(capabilities,
				Capability{Action: ActionTiltOpen, Experimental: true},
				Capability{Action: ActionTiltClose, Experimental: true})
		}
		return capabilities
	case domain.KindScene:
		return []Capability{{Action: ActionActivate}}
	case domain.KindButton:
		return []Capability{{Action: ActionShortPress}, {Action: ActionLongPress}}
	default:
		return nil
	}
}

func isJalousie(device domain.Device) bool {
	haystack := strings.ToLower(device.Category + " " + device.Name)
	for _, keyword := range []string{"jalousie", "raffstore", "lamelle", "lamellen", "venetian", "slat"} {
		if strings.Contains(haystack, keyword) {
			return true
		}
	}
	return false
}

func supports(device domain.Device, action Action) bool {
	for _, capability := range capabilitiesFor(device) {
		if capability.Action == action {
			return true
		}
	}
	return false
}

func (c *Controller) Execute(ctx context.Context, deviceID string, action Action) (CommandResult, error) {
	if c.stopped.Load() {
		return CommandResult{}, ErrControllerStopped
	}
	device, exists := c.devices[deviceID]
	if !exists {
		return CommandResult{}, ErrDeviceNotFound
	}
	if !supports(device, action) {
		return CommandResult{}, ErrUnsupportedAction
	}

	operationCtx, cancel := context.WithTimeout(ctx, c.config.OperationTimeout)
	defer cancel()
	_, err := c.scheduler.command(operationCtx, func(callCtx context.Context) (any, error) {
		return nil, c.executeDevice(callCtx, device, action)
	})
	if err != nil {
		if err != context.Canceled && err != ErrSchedulerStopped {
			c.recordDisconnected()
		}
		return CommandResult{}, err
	}
	c.recordConnected()

	if busAddress, pollable := c.deviceModules[deviceID]; pollable && !c.config.DisableRefresh {
		select {
		case c.refreshModule <- busAddress:
		default:
		}
	}
	return CommandResult{ID: fmt.Sprintf("command-%d", c.commandID.Add(1))}, nil
}

func (c *Controller) executeDevice(ctx context.Context, device domain.Device, action Action) error {
	switch action {
	case ActionOn, ActionOff, ActionToggle:
		busAddress, err := amdBusAddress(device.Ref)
		if err != nil {
			return err
		}
		command := 6
		if action == ActionOn {
			command = 2
		} else if action == ActionOff {
			command = 3
		}
		_, err = c.client.SendTelegram(ctx, 0, busAddress, (device.Ref.Channel<<5)|command)
		return err

	case ActionRaise:
		return c.sendInputPlan(ctx, preferredUpRef(device), domain.ShortPressEvents())
	case ActionLower:
		return c.sendInputPlan(ctx, device.Ref, domain.ShortPressEvents())
	case ActionStop:
		return c.sendInputPlan(ctx, device.Ref, domain.LongPressEvents())
	case ActionTiltOpen:
		return c.sendInputPlan(ctx, preferredUpRef(device), domain.TipEvents())
	case ActionTiltClose:
		return c.sendInputPlan(ctx, device.Ref, domain.TipEvents())
	case ActionActivate, ActionShortPress:
		return c.sendInputPlan(ctx, device.Ref, domain.ShortPressEvents())
	case ActionLongPress:
		return c.sendInputPlan(ctx, device.Ref, domain.LongPressEvents())
	default:
		return ErrUnsupportedAction
	}
}

func preferredUpRef(device domain.Device) domain.ChannelRef {
	if device.UpRef != nil {
		return *device.UpRef
	}
	return device.Ref
}

func (c *Controller) sendInputPlan(ctx context.Context, ref domain.ChannelRef, events []domain.InputEvent) error {
	if ref.ModuleClass != domain.ModuleEMD || ref.DIP < 0 || ref.Channel < 0 {
		return fmt.Errorf("controller: invalid EMD channel reference")
	}
	for _, event := range events {
		if err := c.client.SimInputEvent(ctx, 0, ref.DIP, ref.Channel, int(event), 4); err != nil {
			return err
		}
	}
	return nil
}
