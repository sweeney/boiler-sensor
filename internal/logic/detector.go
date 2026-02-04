package logic

import "time"

// Detector tracks state and detects debounced transitions.
type Detector struct {
	debounceDuration time.Duration
	ch               ChannelState
	hw               ChannelState
	baselined        bool
	startTime        time.Time
	eventCounts      EventCounts
	lastHeartbeat    time.Time
}

// NewDetector creates a new transition detector with the given debounce duration.
// The startTime is used for calculating uptime in heartbeat events.
func NewDetector(debounceDuration time.Duration, startTime time.Time) *Detector {
	return &Detector{
		debounceDuration: debounceDuration,
		startTime:        startTime,
		lastHeartbeat:    startTime,
	}
}

// Process takes a new input sample and returns any events that should be emitted.
// Events are only returned after baseline is established and on state transitions.
func (d *Detector) Process(input Input) []Event {
	chState := boolToState(input.CH)
	hwState := boolToState(input.HW)

	chTransition := d.processChannel(&d.ch, chState, input.Time)
	hwTransition := d.processChannel(&d.hw, hwState, input.Time)

	// Check if we've established baseline
	if !d.baselined {
		if d.ch.Baselined && d.hw.Baselined {
			d.baselined = true
		}
		return nil // No events until baseline established
	}

	var events []Event

	// Emit events for transitions (order: CH first, then HW if both change simultaneously)
	if chTransition != nil {
		events = append(events, Event{
			Timestamp: input.Time,
			Type:      *chTransition,
			CHState:   d.ch.Stable,
			HWState:   d.hw.Stable,
		})
	}

	if hwTransition != nil {
		events = append(events, Event{
			Timestamp: input.Time,
			Type:      *hwTransition,
			CHState:   d.ch.Stable,
			HWState:   d.hw.Stable,
		})
	}

	// Count events
	for _, e := range events {
		switch e.Type {
		case EventCHOn:
			d.eventCounts.CHOn++
		case EventCHOff:
			d.eventCounts.CHOff++
		case EventHWOn:
			d.eventCounts.HWOn++
		case EventHWOff:
			d.eventCounts.HWOff++
		}
	}

	return events
}

// processChannel handles debounce logic for a single channel.
// Returns the event type if a transition occurred, nil otherwise.
func (d *Detector) processChannel(ch *ChannelState, newState State, now time.Time) *EventType {
	// First time seeing this channel
	if !ch.Baselined {
		if ch.Pending == "" {
			// Start observing
			ch.Pending = newState
			ch.PendingSince = now
			return nil
		}

		if ch.Pending != newState {
			// State changed during baseline, restart
			ch.Pending = newState
			ch.PendingSince = now
			return nil
		}

		// Check if debounce period has passed
		if now.Sub(ch.PendingSince) >= d.debounceDuration {
			ch.Stable = newState
			ch.Baselined = true
			ch.Pending = ""
		}
		return nil
	}

	// Already baselined - detect transitions
	if newState == ch.Stable {
		// No change from stable state, clear any pending
		ch.Pending = ""
		return nil
	}

	// State differs from stable
	if ch.Pending != newState {
		// New pending state
		ch.Pending = newState
		ch.PendingSince = now
		return nil
	}

	// Same pending state, check debounce
	if now.Sub(ch.PendingSince) >= d.debounceDuration {
		oldState := ch.Stable
		ch.Stable = newState
		ch.Pending = ""
		return eventTypeForTransition(oldState, newState, ch == &d.ch)
	}

	return nil
}

func boolToState(b bool) State {
	if b {
		return StateOn
	}
	return StateOff
}

func eventTypeForTransition(from, to State, isCH bool) *EventType {
	var event EventType
	if isCH {
		if to == StateOn {
			event = EventCHOn
		} else {
			event = EventCHOff
		}
	} else {
		if to == StateOn {
			event = EventHWOn
		} else {
			event = EventHWOff
		}
	}
	return &event
}

// IsBaselined returns whether the detector has established a baseline.
func (d *Detector) IsBaselined() bool {
	return d.baselined
}

// CurrentState returns the current stable states.
func (d *Detector) CurrentState() (ch State, hw State) {
	return d.ch.Stable, d.hw.Stable
}

// CheckHeartbeat returns heartbeat data if the interval has elapsed since the
// last heartbeat (or startup). Returns nil if not yet baselined, if the
// interval has not elapsed, or if interval is <= 0 (disabled).
func (d *Detector) CheckHeartbeat(now time.Time, interval time.Duration) *HeartbeatData {
	if interval <= 0 {
		return nil
	}

	if !d.baselined {
		return nil
	}

	if now.Sub(d.lastHeartbeat) < interval {
		return nil
	}

	d.lastHeartbeat = now
	return &HeartbeatData{
		Timestamp: now,
		Uptime:    now.Sub(d.startTime),
		Counts:    d.eventCounts,
	}
}
