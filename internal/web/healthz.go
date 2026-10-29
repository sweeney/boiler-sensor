package web

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/sweeney/common/iot/status"
)

// health is the /healthz body. A struct rather than a map so the field order is
// fixed: a person reading it with curl sees status and version first.
type health struct {
	// Status is "ok", "starting" (no baseline yet, so no events can be
	// published) or "degraded" (running, but MQTT is disconnected, so events
	// are being buffered rather than delivered).
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Version string `json:"version"`

	UptimeS   int64  `json:"uptime_s"`
	StartTime string `json:"start_time"`

	Ready         bool              `json:"ready"`
	MQTTConnected bool              `json:"mqtt_connected"`
	Channels      map[string]string `json:"channels"`

	// Memory and goroutines, so "is it leaking?" is one curl rather than an
	// ssh and a /proc read. This daemon runs for months between restarts.
	Goroutines int     `json:"goroutines"`
	HeapMB     float64 `json:"heap_mb"`
	SysMB      float64 `json:"sys_mb"`
}

// healthFor derives the health report from a status snapshot.
func healthFor(snap status.Snapshot, version string) health {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	h := health{
		Status:        "ok",
		Version:       version,
		UptimeS:       int64(snap.Uptime().Truncate(time.Second).Seconds()),
		StartTime:     snap.StartTime.UTC().Format(time.RFC3339),
		Ready:         snap.Baselined,
		MQTTConnected: snap.MQTTConnected,
		Channels:      make(map[string]string, len(snap.Profile.Channels)),
		Goroutines:    runtime.NumGoroutine(),
		HeapMB:        round1(float64(mem.HeapAlloc) / (1 << 20)),
		SysMB:         round1(float64(mem.Sys) / (1 << 20)),
	}
	for _, c := range snap.Profile.Channels {
		s := string(snap.States[c.Key])
		if s == "" {
			s = "UNKNOWN"
		}
		h.Channels[c.Key] = s
	}
	switch {
	case !snap.Baselined:
		h.Status, h.Detail = "starting", "waiting for a stable GPIO baseline"
	case !snap.MQTTConnected:
		h.Status, h.Detail = "degraded", "MQTT disconnected; events are buffered until it reconnects"
	}
	return h
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// Always 200, as pi-price-screen does: the status field carries the
	// state, so a monitor reads one thing rather than guessing from a code.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(healthFor(s.tracker.Snapshot(), s.Version))
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
