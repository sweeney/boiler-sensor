// Package mqtt provides MQTT publishing with abstraction for testing.
package mqtt

import (
	"encoding/json"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

// Topic is the MQTT topic for boiler events.
const Topic = "energy/boiler/sensor/events"

// TopicSystem is the MQTT topic for system lifecycle events.
const TopicSystem = "energy/boiler/sensor/system"

// Publisher publishes events to MQTT.
type Publisher interface {
	// Publish sends a boiler event to the broker.
	// Returns error if publishing fails (should not crash the process).
	Publish(event logic.Event) error

	// PublishSystem sends a system lifecycle event to the broker.
	PublishSystem(event SystemEvent) error

	// Close disconnects from the broker.
	Close() error
}

// ConnectionStatus reports whether the MQTT connection is active.
type ConnectionStatus interface {
	IsConnected() bool
}

// SystemEvent represents a system lifecycle event (e.g., startup, shutdown, heartbeat).
type SystemEvent struct {
	Timestamp  time.Time
	Event      string // e.g., "STARTUP", "SHUTDOWN", "HEARTBEAT"
	Reason     string // e.g., "SIGTERM", "SIGINT" (shutdown only)
	RawPayload []byte // Pre-formatted JSON payload; if set, FormatSystemPayload returns it directly
	Retained   bool   // Whether the message should be retained by the broker
}

// Payload represents the MQTT message payload structure.
type Payload struct {
	Boiler BoilerPayload `json:"boiler"`
}

// BoilerPayload contains the boiler event details.
type BoilerPayload struct {
	Timestamp string       `json:"timestamp"`
	Event     string       `json:"event"`
	CH        ChannelState `json:"ch"`
	HW        ChannelState `json:"hw"`
}

// ChannelState represents a single channel's state.
type ChannelState struct {
	State string `json:"state"`
}

// FormatPayload creates the JSON payload for a boiler event.
func FormatPayload(event logic.Event) ([]byte, error) {
	payload := Payload{
		Boiler: BoilerPayload{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     string(event.Type),
			CH:        ChannelState{State: string(event.CHState)},
			HW:        ChannelState{State: string(event.HWState)},
		},
	}
	return json.Marshal(payload)
}

// SystemPayload represents the MQTT message payload for system events.
// Used for simple events (LWT, RECONNECTED) that don't carry a full status snapshot.
type SystemPayload struct {
	System SystemPayloadInner `json:"system"`
}

// SystemPayloadInner contains the system event details.
type SystemPayloadInner struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Reason    string `json:"reason,omitempty"`
}

// FormatSystemPayload creates the JSON payload for a system event.
// If event.RawPayload is set, it is returned directly (used for full status snapshots).
func FormatSystemPayload(event SystemEvent) ([]byte, error) {
	if event.RawPayload != nil {
		return event.RawPayload, nil
	}

	payload := SystemPayload{
		System: SystemPayloadInner{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     event.Event,
			Reason:    event.Reason,
		},
	}
	return json.Marshal(payload)
}
