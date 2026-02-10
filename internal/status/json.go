package status

import (
	"encoding/json"
	"time"
)

// StatusJSON is the top-level JSON envelope for status output.
type StatusJSON struct {
	Status StatusInner `json:"status"`
}

// StatusInner contains the status details.
type StatusInner struct {
	Event         string       `json:"event,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	CH            string       `json:"ch"`
	HW            string       `json:"hw"`
	Ready         bool         `json:"ready"`
	UptimeSeconds int64        `json:"uptime_seconds"`
	StartTime     string       `json:"start_time"`
	Timestamp     string       `json:"timestamp"`
	MQTT          MQTTStatus   `json:"mqtt"`
	Counts        CountsJSON   `json:"event_counts"`
	Network       *NetworkJSON `json:"network,omitempty"`
	Config        ConfigJSON   `json:"config"`
}

// MQTTStatus reports MQTT connection state.
type MQTTStatus struct {
	Connected bool   `json:"connected"`
	Broker    string `json:"broker"`
}

// CountsJSON is the JSON representation of event counts.
type CountsJSON struct {
	CHOn  int `json:"ch_on"`
	CHOff int `json:"ch_off"`
	HWOn  int `json:"hw_on"`
	HWOff int `json:"hw_off"`
}

// NetworkJSON is the JSON representation of network info.
type NetworkJSON struct {
	Type       string `json:"type"`
	IP         string `json:"ip"`
	Status     string `json:"status"`
	Gateway    string `json:"gateway"`
	WifiStatus string `json:"wifi_status"`
	SSID       string `json:"ssid"`
}

// ConfigJSON is the JSON representation of daemon config.
type ConfigJSON struct {
	PollMs      int64  `json:"poll_ms"`
	DebounceMs  int64  `json:"debounce_ms"`
	HeartbeatMs int64  `json:"heartbeat_ms"`
	Broker      string `json:"broker"`
	HTTPPort    string `json:"http_port"`
	WSBroker    string `json:"ws_broker,omitempty"`
}

func buildInner(snap Snapshot) StatusInner {
	ch := string(snap.CH)
	if ch == "" {
		ch = "UNKNOWN"
	}
	hw := string(snap.HW)
	if hw == "" {
		hw = "UNKNOWN"
	}

	return StatusInner{
		CH:            ch,
		HW:            hw,
		Ready:         snap.Baselined,
		UptimeSeconds: int64(snap.Uptime().Truncate(time.Second).Seconds()),
		StartTime:     snap.StartTime.UTC().Format(time.RFC3339),
		Timestamp:     snap.Now.UTC().Format(time.RFC3339),
		MQTT:          MQTTStatus{Connected: snap.MQTTConnected, Broker: snap.Config.Broker},
		Counts: CountsJSON{
			CHOn:  snap.Counts.CHOn,
			CHOff: snap.Counts.CHOff,
			HWOn:  snap.Counts.HWOn,
			HWOff: snap.Counts.HWOff,
		},
		Config: ConfigJSON{
			PollMs:      snap.Config.PollMs,
			DebounceMs:  snap.Config.DebounceMs,
			HeartbeatMs: snap.Config.HeartbeatMs,
			Broker:      snap.Config.Broker,
			HTTPPort:    snap.Config.HTTPPort,
			WSBroker:    snap.Config.WSBroker,
		},
	}
}

func buildNetwork(snap Snapshot, inner *StatusInner) {
	if snap.Network != nil {
		inner.Network = &NetworkJSON{
			Type:       snap.Network.Type,
			IP:         snap.Network.IP,
			Status:     snap.Network.Status,
			Gateway:    snap.Network.Gateway,
			WifiStatus: snap.Network.WifiStatus,
			SSID:       snap.Network.SSID,
		}
	}
}

// FormatJSON returns the JSON status for the web endpoint (no event/reason).
func FormatJSON(snap Snapshot) []byte {
	inner := buildInner(snap)
	buildNetwork(snap, &inner)

	data, _ := json.MarshalIndent(StatusJSON{Status: inner}, "", "  ")
	return data
}

// FormatStatusEvent returns the JSON status for an MQTT system event.
func FormatStatusEvent(snap Snapshot, event, reason string) []byte {
	inner := buildInner(snap)
	inner.Event = event
	inner.Reason = reason
	buildNetwork(snap, &inner)

	data, _ := json.Marshal(StatusJSON{Status: inner})
	return data
}
