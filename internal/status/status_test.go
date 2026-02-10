package status

import (
	"sync"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

func TestNewTracker(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{PollMs: 100, DebounceMs: 250, Broker: "tcp://localhost:1883", HTTPAddr: ":80"}
	tr := NewTracker(start, cfg)

	snap := tr.Snapshot()
	if !snap.StartTime.Equal(start) {
		t.Errorf("StartTime: got %v, want %v", snap.StartTime, start)
	}
	if snap.Config.PollMs != 100 {
		t.Errorf("Config.PollMs: got %d, want 100", snap.Config.PollMs)
	}
	if snap.Config.HTTPAddr != ":80" {
		t.Errorf("Config.HTTPAddr: got %q, want %q", snap.Config.HTTPAddr, ":80")
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
