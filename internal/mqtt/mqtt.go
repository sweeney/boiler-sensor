// Package mqtt provides MQTT publishing with abstraction for testing.
package mqtt

import (
	"encoding/json"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

// Topic is the MQTT topic for heating events.
const Topic = "energy/BOILER_SENSOR/SENSOR/heating"

// TopicSystem is the MQTT topic for system lifecycle events.
const TopicSystem = "energy/BOILER_SENSOR/SENSOR/system"

// Publisher publishes events to MQTT.
type Publisher interface {
	// Publish sends a heating event to the broker.
	// Returns error if publishing fails (should not crash the process).
	Publish(event logic.Event) error

	// PublishSystem sends a system lifecycle event to the broker.
	PublishSystem(event SystemEvent) error

	// Close disconnects from the broker.
	Close() error
}

// SystemEvent represents a system lifecycle event (e.g., startup, shutdown, heartbeat).
type SystemEvent struct {
	Timestamp time.Time
	Event     string        // e.g., "STARTUP", "SHUTDOWN", "HEARTBEAT"
	Reason    string        // e.g., "SIGTERM", "SIGINT" (shutdown only)
	Config    *SystemConfig // Configuration info (startup only)
	Heartbeat *HeartbeatInfo // Heartbeat info (heartbeat only)
	Network   *NetworkInfo  // Network info from pi-helper (startup/heartbeat)
}

// HeartbeatInfo contains information for a heartbeat event.
type HeartbeatInfo struct {
	UptimeSeconds int64
	EventCounts   HeartbeatCounts
}

// HeartbeatCounts tracks the number of each event type since startup.
type HeartbeatCounts struct {
	CHOn  int `json:"ch_on"`
	CHOff int `json:"ch_off"`
	HWOn  int `json:"hw_on"`
	HWOff int `json:"hw_off"`
}

// NetworkInfo contains network state from pi-helper.
type NetworkInfo struct {
	Type       string
	IP         string
	Status     string
	Gateway    string
	WifiStatus string
	SSID       string
}

// SystemConfig contains daemon configuration for startup events.
type SystemConfig struct {
	PollMs      int64  // Polling interval in milliseconds
	DebounceMs  int64  // Debounce duration in milliseconds
	HeartbeatMs int64  // Heartbeat interval in milliseconds (0 = disabled)
	Broker      string // MQTT broker address
}

// Payload represents the MQTT message payload structure.
type Payload struct {
	Heating HeatingPayload `json:"heating"`
}

// HeatingPayload contains the heating event details.
type HeatingPayload struct {
	Timestamp string       `json:"timestamp"`
	Event     string       `json:"event"`
	CH        ChannelState `json:"ch"`
	HW        ChannelState `json:"hw"`
}

// ChannelState represents a single channel's state.
type ChannelState struct {
	State string `json:"state"`
}

// FormatPayload creates the JSON payload for a heating event.
func FormatPayload(event logic.Event) ([]byte, error) {
	payload := Payload{
		Heating: HeatingPayload{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     string(event.Type),
			CH:        ChannelState{State: string(event.CHState)},
			HW:        ChannelState{State: string(event.HWState)},
		},
	}
	return json.Marshal(payload)
}

// SystemPayload represents the MQTT message payload for system events.
type SystemPayload struct {
	System SystemPayloadInner `json:"system"`
}

// SystemPayloadInner contains the system event details.
type SystemPayloadInner struct {
	Timestamp string                  `json:"timestamp"`
	Event     string                  `json:"event"`
	Reason    string                  `json:"reason,omitempty"`
	Config    *SystemConfigPayload    `json:"config,omitempty"`
	Heartbeat *HeartbeatPayload       `json:"heartbeat,omitempty"`
	Network   *NetworkPayload         `json:"network,omitempty"`
}

// HeartbeatPayload is the JSON representation of HeartbeatInfo.
type HeartbeatPayload struct {
	UptimeSeconds int64           `json:"uptime_seconds"`
	EventCounts   HeartbeatCounts `json:"event_counts"`
}

// NetworkPayload is the JSON representation of NetworkInfo.
type NetworkPayload struct {
	Type       string `json:"type"`
	IP         string `json:"ip"`
	Status     string `json:"status"`
	Gateway    string `json:"gateway"`
	WifiStatus string `json:"wifi_status"`
	SSID       string `json:"ssid"`
}

// SystemConfigPayload is the JSON representation of SystemConfig.
type SystemConfigPayload struct {
	PollMs      int64  `json:"poll_ms"`
	DebounceMs  int64  `json:"debounce_ms"`
	HeartbeatMs int64  `json:"heartbeat_ms"`
	Broker      string `json:"broker"`
}

// FormatSystemPayload creates the JSON payload for a system event.
func FormatSystemPayload(event SystemEvent) ([]byte, error) {
	inner := SystemPayloadInner{
		Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
		Event:     event.Event,
		Reason:    event.Reason,
	}

	if event.Config != nil {
		inner.Config = &SystemConfigPayload{
			PollMs:      event.Config.PollMs,
			DebounceMs:  event.Config.DebounceMs,
			HeartbeatMs: event.Config.HeartbeatMs,
			Broker:      event.Config.Broker,
		}
	}

	if event.Heartbeat != nil {
		inner.Heartbeat = &HeartbeatPayload{
			UptimeSeconds: event.Heartbeat.UptimeSeconds,
			EventCounts:   event.Heartbeat.EventCounts,
		}
	}

	if event.Network != nil {
		inner.Network = &NetworkPayload{
			Type:       event.Network.Type,
			IP:         event.Network.IP,
			Status:     event.Network.Status,
			Gateway:    event.Network.Gateway,
			WifiStatus: event.Network.WifiStatus,
			SSID:       event.Network.SSID,
		}
	}

	payload := SystemPayload{System: inner}
	return json.Marshal(payload)
}
