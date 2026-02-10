// Package status provides a thread-safe status tracker for the boiler-sensor daemon.
// It is designed to be read by HTTP handlers and (future) LED drivers.
package status

import (
	"sync"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

// NetworkInfo contains network state. This is a local copy to avoid
// importing internal/mqtt from status.
type NetworkInfo struct {
	Type       string
	IP         string
	Status     string
	Gateway    string
	WifiStatus string
	SSID       string
}

// Config contains daemon configuration for display.
type Config struct {
	PollMs      int64
	DebounceMs  int64
	HeartbeatMs int64
	Broker      string
	HTTPPort    string
	WSBroker    string // Websocket broker URL for browser MQTT (empty = disabled)
}

// Snapshot is a point-in-time view of daemon state.
// It is a value type — safe to use after the lock is released.
type Snapshot struct {
	CH            logic.State
	HW            logic.State
	Baselined     bool
	Counts        logic.EventCounts
	StartTime     time.Time
	Now           time.Time
	MQTTConnected bool
	Network       *NetworkInfo
	Config        Config
}

// Uptime returns the duration since the daemon started.
func (s Snapshot) Uptime() time.Duration {
	return s.Now.Sub(s.StartTime)
}

// Tracker holds mutable daemon state behind an RWMutex.
type Tracker struct {
	mu   sync.RWMutex
	snap Snapshot
}

// NewTracker creates a Tracker with the given start time and config.
func NewTracker(startTime time.Time, cfg Config) *Tracker {
	return &Tracker{
		snap: Snapshot{
			StartTime: startTime,
			Config:    cfg,
		},
	}
}

// Update sets channel states, baseline status, and event counts.
// Called from runLoop on every tick.
func (t *Tracker) Update(ch, hw logic.State, baselined bool, counts logic.EventCounts) {
	t.mu.Lock()
	t.snap.CH = ch
	t.snap.HW = hw
	t.snap.Baselined = baselined
	t.snap.Counts = counts
	t.mu.Unlock()
}

// SetMQTTConnected sets the MQTT connection status.
func (t *Tracker) SetMQTTConnected(connected bool) {
	t.mu.Lock()
	t.snap.MQTTConnected = connected
	t.mu.Unlock()
}

// SetNetwork sets the network info.
func (t *Tracker) SetNetwork(info *NetworkInfo) {
	t.mu.Lock()
	t.snap.Network = info
	t.mu.Unlock()
}

// Snapshot returns a point-in-time copy of the daemon state.
// The Now field is set to the current time at the moment of the call.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	s := t.snap
	t.mu.RUnlock()
	s.Now = time.Now()
	return s
}
