package controller

import (
	"context"
	"fmt"
	"time"
)

func (c *Controller) pollLoop() {
	defer c.wg.Done()
	pollTimer := time.NewTimer(time.Hour)
	graceTimer := time.NewTimer(time.Hour)
	healthTimer := time.NewTimer(time.Hour)
	stopTimer(pollTimer)
	stopTimer(graceTimer)
	stopTimer(healthTimer)
	defer pollTimer.Stop()
	defer graceTimer.Stop()
	defer healthTimer.Stop()

	// Idle health only makes sense when enabled and there is a module to probe.
	idleHealth := c.config.IdleHealthInterval > 0 && len(c.moduleAddresses) > 0

	active := false
	var seenEpoch uint64

	// Start disconnected: if idle health is on, begin probing immediately.
	if idleHealth {
		resetTimer(healthTimer, c.config.IdleHealthInterval)
	}

	for {
		select {
		case <-c.ctx.Done():
			return

		case <-c.pollWake:
			count, epoch := c.subscriberState()
			if count > 0 {
				stopTimer(graceTimer)
				if epoch != seenEpoch {
					seenEpoch = epoch
					active = true
					stopTimer(healthTimer)
					c.pollSweep()
					resetTimer(pollTimer, c.config.ActivePollInterval)
				}
			} else if active {
				resetTimer(graceTimer, c.config.SubscriberGrace)
			}

		case <-pollTimer.C:
			if active {
				c.pollSweep()
				resetTimer(pollTimer, c.config.ActivePollInterval)
			}

		case <-graceTimer.C:
			count, _ := c.subscriberState()
			if count == 0 {
				active = false
				stopTimer(pollTimer)
				if idleHealth {
					resetTimer(healthTimer, c.config.IdleHealthInterval)
				}
			}

		case <-healthTimer.C:
			if !active && idleHealth {
				// One lightweight probe keeps connection status current for
				// health checks without resuming full polling.
				_ = c.pollModule(c.moduleAddresses[0])
				resetTimer(healthTimer, c.config.IdleHealthInterval)
			}

		case busAddress := <-c.refreshModule:
			c.pollModule(busAddress)
		}
	}
}

func (c *Controller) pollSweep() {
	complete := true
	for _, busAddress := range c.moduleAddresses {
		if c.ctx.Err() != nil {
			return
		}
		if err := c.pollModule(busAddress); err != nil {
			complete = false
			break
		}
	}
	if complete {
		c.setStale(false)
	}
}

func (c *Controller) pollModule(busAddress int) error {
	operationCtx, cancel := context.WithTimeout(c.ctx, c.config.OperationTimeout)
	defer cancel()
	value, err := c.scheduler.poll(operationCtx, func(callCtx context.Context) (any, error) {
		response, err := c.client.SendTelegram(callCtx, 0, busAddress, 1)
		if err != nil {
			return nil, err
		}
		if len(response) != 5 {
			return nil, fmt.Errorf("controller: module state reply has %d values, want 5", len(response))
		}
		return response[4], nil
	})
	if err != nil {
		if err != context.Canceled && err != ErrSchedulerStopped {
			c.recordDisconnected()
		}
		return err
	}
	bitmask, ok := value.(int)
	if !ok {
		c.recordDisconnected()
		return fmt.Errorf("controller: module state result is %T, not int", value)
	}
	c.recordConnected()
	c.updateModuleState(busAddress, bitmask)
	return nil
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	stopTimer(timer)
	timer.Reset(duration)
}
