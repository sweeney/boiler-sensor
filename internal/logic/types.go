// Package logic contains pure business logic for heating system state tracking.
// This package has NO external dependencies (no GPIO, MQTT, OS, or time.Sleep).
// Time is always injectable via time.Time parameters.
package logic

import "time"

// State represents the logical state of a heating channel.
type State string

const (
	StateOn  State = "ON"
	StateOff State = "OFF"
)

// EventType represents a state transition event.
type EventType string

const (
	EventCHOn  EventType = "CH_ON"
	EventCHOff EventType = "CH_OFF"
	EventHWOn  EventType = "HW_ON"
	EventHWOff EventType = "HW_OFF"
)

// Event represents a state transition to be published.
type Event struct {
	Timestamp time.Time
	Type      EventType
	CHState   State
	HWState   State
}

// ChannelState tracks debounce state for a single channel.
type ChannelState struct {
	// Current stable (debounced) state
	Stable State
	// Pending state during debounce
	Pending State
	// Time when pending state was first observed
	PendingSince time.Time
	// Whether we have established a baseline
	Baselined bool
}

// Input represents a single sample of logical states.
type Input struct {
	CH   bool // true = ON, false = OFF (already inverted from raw GPIO)
	HW   bool
	Time time.Time
}

// EventCounts tracks the number of each event type since startup.
type EventCounts struct {
	CHOn  int
	CHOff int
	HWOn  int
	HWOff int
}

// HeartbeatData contains information for a heartbeat event.
type HeartbeatData struct {
	Timestamp time.Time
	Uptime    time.Duration
	Counts    EventCounts
}
