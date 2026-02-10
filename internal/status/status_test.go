package status

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

func TestNewTracker(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{PollMs: 100, DebounceMs: 250, Broker: "tcp://localhost:1883", HTTPPort: ":80"}
	tr := NewTracker(start, cfg)

	snap := tr.Snapshot()
	if !snap.StartTime.Equal(start) {
		t.Errorf("StartTime: got %v, want %v", snap.StartTime, start)
	}
	if snap.Config.PollMs != 100 {
		t.Errorf("Config.PollMs: got %d, want 100", snap.Config.PollMs)
	}
	if snap.Config.HTTPPort != ":80" {
		t.Errorf("Config.HTTPPort: got %q, want %q", snap.Config.HTTPPort, ":80")
	}
	if snap.Baselined {
		t.Error("expected Baselined=false initially")
	}
	if snap.MQTTConnected {
		t.Error("expected MQTTConnected=false initially")
	}
}

func TestUpdateAndSnapshot(t *testing.T) {
	tr := NewTracker(time.Now(), Config{})

	tr.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{CHOn: 3, HWOff: 1})

	snap := tr.Snapshot()
	if snap.CH != logic.StateOn {
		t.Errorf("CH: got %q, want ON", snap.CH)
	}
	if snap.HW != logic.StateOff {
		t.Errorf("HW: got %q, want OFF", snap.HW)
	}
	if !snap.Baselined {
		t.Error("expected Baselined=true")
	}
	if snap.Counts.CHOn != 3 {
		t.Errorf("Counts.CHOn: got %d, want 3", snap.Counts.CHOn)
	}
	if snap.Counts.HWOff != 1 {
		t.Errorf("Counts.HWOff: got %d, want 1", snap.Counts.HWOff)
	}
}

func TestSetMQTTConnected(t *testing.T) {
	tr := NewTracker(time.Now(), Config{})

	tr.SetMQTTConnected(true)
	if !tr.Snapshot().MQTTConnected {
		t.Error("expected MQTTConnected=true")
	}

	tr.SetMQTTConnected(false)
	if tr.Snapshot().MQTTConnected {
		t.Error("expected MQTTConnected=false")
	}
}

func TestSetNetwork(t *testing.T) {
	tr := NewTracker(time.Now(), Config{})

	if tr.Snapshot().Network != nil {
		t.Error("expected nil Network initially")
	}

	net := &NetworkInfo{Type: "wifi", IP: "192.168.1.42", Status: "connected"}
	tr.SetNetwork(net)

	snap := tr.Snapshot()
	if snap.Network == nil {
		t.Fatal("expected non-nil Network")
	}
	if snap.Network.IP != "192.168.1.42" {
		t.Errorf("Network.IP: got %q, want %q", snap.Network.IP, "192.168.1.42")
	}
}

func TestSnapshotUptime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		StartTime: start,
		Now:       start.Add(15 * time.Minute),
	}

	if snap.Uptime() != 15*time.Minute {
		t.Errorf("Uptime: got %v, want 15m", snap.Uptime())
	}
}

func TestSnapshotNowIsSet(t *testing.T) {
	tr := NewTracker(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Config{})

	before := time.Now()
	snap := tr.Snapshot()
	after := time.Now()

	if snap.Now.Before(before) || snap.Now.After(after) {
		t.Errorf("Now (%v) not between %v and %v", snap.Now, before, after)
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	tr := NewTracker(time.Now(), Config{})
	tr.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{CHOn: 1})

	snap1 := tr.Snapshot()

	tr.Update(logic.StateOff, logic.StateOn, true, logic.EventCounts{CHOn: 1, CHOff: 1})

	// snap1 should still reflect old state
	if snap1.CH != logic.StateOn {
		t.Error("snapshot should be a copy; CH was modified")
	}
	if snap1.HW != logic.StateOff {
		t.Error("snapshot should be a copy; HW was modified")
	}
}

func TestFormatJSON(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		CH:            logic.StateOn,
		HW:            logic.StateOff,
		Baselined:     true,
		Counts:        logic.EventCounts{CHOn: 5, CHOff: 2, HWOn: 1, HWOff: 0},
		StartTime:     start,
		Now:           start.Add(15 * time.Minute),
		MQTTConnected: true,
		Config:        Config{PollMs: 100, DebounceMs: 250, HeartbeatMs: 900000, Broker: "tcp://localhost:1883", HTTPPort: ":80"},
	}

	data := FormatJSON(snap)

	var parsed StatusJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Status.CH != "ON" {
		t.Errorf("CH: got %q, want ON", parsed.Status.CH)
	}
	if parsed.Status.HW != "OFF" {
		t.Errorf("HW: got %q, want OFF", parsed.Status.HW)
	}
	if !parsed.Status.Ready {
		t.Error("expected Ready=true")
	}
	if parsed.Status.UptimeSeconds != 900 {
		t.Errorf("UptimeSeconds: got %d, want 900", parsed.Status.UptimeSeconds)
	}
	if parsed.Status.MQTT.Connected != true {
		t.Error("expected MQTT.Connected=true")
	}
	if parsed.Status.Counts.CHOn != 5 {
		t.Errorf("Counts.CHOn: got %d, want 5", parsed.Status.Counts.CHOn)
	}
	// Event and Reason should be omitted
	if parsed.Status.Event != "" {
		t.Errorf("expected empty Event for web format, got %q", parsed.Status.Event)
	}
	if parsed.Status.Reason != "" {
		t.Errorf("expected empty Reason for web format, got %q", parsed.Status.Reason)
	}
}

func TestFormatJSONUnknownState(t *testing.T) {
	snap := Snapshot{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
	}

	data := FormatJSON(snap)

	var parsed StatusJSON
	json.Unmarshal(data, &parsed)

	if parsed.Status.CH != "UNKNOWN" {
		t.Errorf("CH: got %q, want UNKNOWN", parsed.Status.CH)
	}
	if parsed.Status.HW != "UNKNOWN" {
		t.Errorf("HW: got %q, want UNKNOWN", parsed.Status.HW)
	}
}

func TestFormatStatusEvent(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		CH:            logic.StateOn,
		HW:            logic.StateOff,
		Baselined:     true,
		Counts:        logic.EventCounts{CHOn: 3},
		StartTime:     start,
		Now:           start.Add(15 * time.Minute),
		MQTTConnected: true,
		Config:        Config{PollMs: 100, DebounceMs: 250, Broker: "tcp://localhost:1883"},
	}

	data := FormatStatusEvent(snap, "HEARTBEAT", "")

	var parsed StatusJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Status.Event != "HEARTBEAT" {
		t.Errorf("Event: got %q, want HEARTBEAT", parsed.Status.Event)
	}
	if parsed.Status.Reason != "" {
		t.Errorf("Reason: got %q, want empty", parsed.Status.Reason)
	}
	if parsed.Status.CH != "ON" {
		t.Errorf("CH: got %q, want ON", parsed.Status.CH)
	}
	if parsed.Status.UptimeSeconds != 900 {
		t.Errorf("UptimeSeconds: got %d, want 900", parsed.Status.UptimeSeconds)
	}
}

func TestFormatStatusEventShutdown(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := Snapshot{
		CH:        logic.StateOff,
		HW:        logic.StateOff,
		Baselined: true,
		StartTime: start,
		Now:       start.Add(30 * time.Minute),
		Config:    Config{Broker: "tcp://localhost:1883"},
	}

	data := FormatStatusEvent(snap, "SHUTDOWN", "SIGTERM")

	var parsed StatusJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Status.Event != "SHUTDOWN" {
		t.Errorf("Event: got %q, want SHUTDOWN", parsed.Status.Event)
	}
	if parsed.Status.Reason != "SIGTERM" {
		t.Errorf("Reason: got %q, want SIGTERM", parsed.Status.Reason)
	}
}

func TestFormatStatusEventOmitsReasonWhenEmpty(t *testing.T) {
	snap := Snapshot{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
	}

	data := FormatStatusEvent(snap, "STARTUP", "")

	// Verify "reason" is not in the raw JSON output
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	status := raw["status"].(map[string]interface{})
	if _, exists := status["reason"]; exists {
		t.Error("reason should be omitted when empty")
	}
	if status["event"] != "STARTUP" {
		t.Errorf("event: got %v, want STARTUP", status["event"])
	}
}

func TestFormatJSONWithNetwork(t *testing.T) {
	snap := Snapshot{
		CH:        logic.StateOn,
		HW:        logic.StateOff,
		Baselined: true,
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
		Network:   &NetworkInfo{Type: "wifi", IP: "192.168.1.42", Status: "connected", SSID: "MyNet"},
		Config:    Config{Broker: "tcp://localhost:1883"},
	}

	data := FormatJSON(snap)

	var parsed StatusJSON
	json.Unmarshal(data, &parsed)

	if parsed.Status.Network == nil {
		t.Fatal("expected Network in JSON")
	}
	if parsed.Status.Network.IP != "192.168.1.42" {
		t.Errorf("Network.IP: got %q, want 192.168.1.42", parsed.Status.Network.IP)
	}
	if parsed.Status.Network.SSID != "MyNet" {
		t.Errorf("Network.SSID: got %q, want MyNet", parsed.Status.Network.SSID)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tr := NewTracker(time.Now(), Config{})
	var wg sync.WaitGroup

	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tr.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{CHOn: i})
			tr.SetMQTTConnected(i%2 == 0)
			tr.SetNetwork(&NetworkInfo{IP: "1.2.3.4"})
		}
	}()

	// Reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			snap := tr.Snapshot()
			_ = snap.Uptime()
		}
	}()

	wg.Wait()
}
