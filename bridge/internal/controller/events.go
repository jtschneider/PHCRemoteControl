package controller

func (c *Controller) setStale(stale bool) {
	c.stateMu.Lock()
	if c.stale == stale {
		c.stateMu.Unlock()
		return
	}
	c.stale = stale
	c.revision++
	event := Event{
		Kind:       EventConnection,
		Revision:   c.revision,
		Connection: c.connection,
		Stale:      c.stale,
	}
	c.stateMu.Unlock()
	c.publish(event)
}

func (c *Controller) recordConnected() {
	c.stateMu.Lock()
	if c.connection == ConnectionConnected {
		c.stateMu.Unlock()
		return
	}
	c.connection = ConnectionConnected
	c.revision++
	event := Event{
		Kind:       EventConnection,
		Revision:   c.revision,
		Connection: c.connection,
		Stale:      c.stale,
	}
	c.stateMu.Unlock()
	c.publish(event)
}

func (c *Controller) recordDisconnected() {
	c.stateMu.Lock()
	if c.connection == ConnectionDisconnected && c.stale {
		c.stateMu.Unlock()
		return
	}
	c.connection = ConnectionDisconnected
	c.stale = true
	c.revision++
	event := Event{
		Kind:       EventConnection,
		Revision:   c.revision,
		Connection: c.connection,
		Stale:      true,
	}
	c.stateMu.Unlock()
	c.publish(event)
}

func (c *Controller) updateModuleState(busAddress, bitmask int) {
	var events []Event
	c.stateMu.Lock()
	for _, channel := range c.modules[busAddress] {
		power := PowerOff
		if (uint64(bitmask)>>uint(channel.channel))&1 == 1 {
			power = PowerOn
		}
		state := DeviceState{Power: power}
		if current, exists := c.states[channel.deviceID]; exists && current == state {
			continue
		}
		c.states[channel.deviceID] = state
		c.revision++
		stateCopy := state
		events = append(events, Event{
			Kind:     EventState,
			Revision: c.revision,
			DeviceID: channel.deviceID,
			State:    &stateCopy,
		})
	}
	c.stateMu.Unlock()
	for _, event := range events {
		c.publish(event)
	}
}
