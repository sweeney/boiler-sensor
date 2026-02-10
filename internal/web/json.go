package web

import (
	"encoding/json"
	"time"

	"github.com/sweeney/boiler-sensor/internal/status"
)

// StatusJSON is the JSON representation of the daemon status.
type StatusJSON struct {
	Status    StatusInner `json:"status"`
}

// StatusInner contains the status details.
type StatusInner struct {
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
	HTTPAddr    string `json:"http_addr"`
}

func formatJSON(snap status.Snapshot) []byte {
	ch := string(snap.CH)
	if ch == "" {
		ch = "UNKNOWN"
	}
	hw := string(snap.HW)
	if hw == "" {
		hw = "UNKNOWN"
	}

	sj := StatusJSON{
		Status: StatusInner{
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
				HTTPAddr:    snap.Config.HTTPAddr,
			},
		},
	}

	if snap.Network != nil {
		sj.Status.Network = &NetworkJSON{
			Type:       snap.Network.Type,
			IP:         snap.Network.IP,
			Status:     snap.Network.Status,
			Gateway:    snap.Network.Gateway,
			WifiStatus: snap.Network.WifiStatus,
			SSID:       snap.Network.SSID,
		}
	}

	data, _ := json.MarshalIndent(sj, "", "  ")
	return data
}
