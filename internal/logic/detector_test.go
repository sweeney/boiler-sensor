package logic

import (
	"testing"
	"time"
)

func TestNewDetector(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)
	if d == nil {
		t.Fatal("NewDetector returned nil")
	}
	if d.debounceDuration != 250*time.Millisecond {
		t.Errorf("expected debounce duration 250ms, got %v", d.debounceDuration)
	}
	if d.baselined {
		t.Error("new detector should not be baselined")
	}
	if !d.startTime.Equal(startTime) {
		t.Errorf("expected startTime %v, got %v", startTime, d.startTime)
	}
	if !d.lastHeartbeat.Equal(startTime) {
		t.Errorf("expected lastHeartbeat %v, got %v", startTime, d.lastHeartbeat)
	}
}

func TestBaselineEstablishment(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, now)

	// First sample - starts observation
	events := d.Process(Input{CH: true, HW: false, Time: now})
	if len(events) != 0 {
		t.Errorf("expected no events during baseline, got %d", len(events))
	}
	if d.IsBaselined() {
		t.Error("should not be baselined after first sample")
	}

	// Before debounce period
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(200 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events during baseline, got %d", len(events))
	}
	if d.IsBaselined() {
		t.Error("should not be baselined before debounce period")
	}

	// After debounce period - baseline established
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events at baseline establishment, got %d", len(events))
	}
	if !d.IsBaselined() {
		t.Error("should be baselined after debounce period")
	}

	ch, hw := d.CurrentState()
	if ch != StateOn {
		t.Errorf("expected CH=ON, got %s", ch)
	}
	if hw != StateOff {
		t.Errorf("expected HW=OFF, got %s", hw)
	}
}

func TestBaselineResetOnChange(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, now)

	// Start observation with CH=ON
	d.Process(Input{CH: true, HW: false, Time: now})

	// Change state before debounce completes
	d.Process(Input{CH: false, HW: false, Time: now.Add(100 * time.Millisecond)})

	// Wait full debounce from original time - should NOT baseline because state changed
	events := d.Process(Input{CH: false, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
	// CH should NOT be baselined yet because timer reset
	// But HW should be baselined (it was stable the whole time)

	// Wait full debounce from the state change
	events = d.Process(Input{CH: false, HW: false, Time: now.Add(350 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events at baseline establishment, got %d", len(events))
	}
	if !d.IsBaselined() {
		t.Error("should be baselined after debounce from state change")
	}

	ch, hw := d.CurrentState()
	if ch != StateOff {
		t.Errorf("expected CH=OFF, got %s", ch)
	}
	if hw != StateOff {
		t.Errorf("expected HW=OFF, got %s", hw)
	}
}

func TestNoEventsForStableState(t *testing.T) {
	d := setupBaselinedDetector(t, true, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// Send same state multiple times
	for i := 0; i < 10; i++ {
		events := d.Process(Input{CH: true, HW: false, Time: now.Add(time.Duration(i) * 100 * time.Millisecond)})
		if len(events) != 0 {
			t.Errorf("iteration %d: expected no events for stable state, got %d", i, len(events))
		}
	}
}

func TestSingleTransitionCHOnToOff(t *testing.T) {
	d := setupBaselinedDetector(t, true, false) // CH=ON, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// CH changes to OFF
	events := d.Process(Input{CH: false, HW: false, Time: now})
	if len(events) != 0 {
		t.Errorf("expected no events before debounce, got %d", len(events))
	}

	// Still pending
	events = d.Process(Input{CH: false, HW: false, Time: now.Add(200 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events before debounce, got %d", len(events))
	}

	// Debounce complete
	events = d.Process(Input{CH: false, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after debounce, got %d", len(events))
	}

	e := events[0]
	if e.Type != EventCHOff {
		t.Errorf("expected CH_OFF event, got %s", e.Type)
	}
	if e.CHState != StateOff {
		t.Errorf("expected CHState=OFF, got %s", e.CHState)
	}
	if e.HWState != StateOff {
		t.Errorf("expected HWState=OFF, got %s", e.HWState)
	}
	if !e.Timestamp.Equal(now.Add(250 * time.Millisecond)) {
		t.Errorf("unexpected timestamp: %v", e.Timestamp)
	}
}

func TestSingleTransitionCHOffToOn(t *testing.T) {
	d := setupBaselinedDetector(t, false, false) // CH=OFF, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// CH changes to ON
	d.Process(Input{CH: true, HW: false, Time: now})

	// Debounce complete
	events := d.Process(Input{CH: true, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventCHOn {
		t.Errorf("expected CH_ON event, got %s", events[0].Type)
	}
	if events[0].CHState != StateOn {
		t.Errorf("expected CHState=ON, got %s", events[0].CHState)
	}
}

func TestSingleTransitionHWOnToOff(t *testing.T) {
	d := setupBaselinedDetector(t, false, true) // CH=OFF, HW=ON
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// HW changes to OFF
	d.Process(Input{CH: false, HW: false, Time: now})

	// Debounce complete
	events := d.Process(Input{CH: false, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventHWOff {
		t.Errorf("expected HW_OFF event, got %s", events[0].Type)
	}
	if events[0].HWState != StateOff {
		t.Errorf("expected HWState=OFF, got %s", events[0].HWState)
	}
}

func TestSingleTransitionHWOffToOn(t *testing.T) {
	d := setupBaselinedDetector(t, false, false) // CH=OFF, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// HW changes to ON
	d.Process(Input{CH: false, HW: true, Time: now})

	// Debounce complete
	events := d.Process(Input{CH: false, HW: true, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventHWOn {
		t.Errorf("expected HW_ON event, got %s", events[0].Type)
	}
	if events[0].HWState != StateOn {
		t.Errorf("expected HWState=ON, got %s", events[0].HWState)
	}
}

func TestBounceShorterThanDebounce(t *testing.T) {
	d := setupBaselinedDetector(t, true, false) // CH=ON, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// CH bounces to OFF
	events := d.Process(Input{CH: false, HW: false, Time: now})
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}

	// Bounce back to ON before debounce completes
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(100 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}

	// Wait past original debounce time - should NOT trigger because state returned to stable
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(300 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events after bounce, got %d", len(events))
	}

	// Verify state unchanged
	ch, _ := d.CurrentState()
	if ch != StateOn {
		t.Errorf("expected CH=ON after bounce, got %s", ch)
	}
}

func TestMultipleBounces(t *testing.T) {
	d := setupBaselinedDetector(t, false, false) // CH=OFF, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// Multiple rapid bounces
	states := []bool{true, false, true, false, true}
	for i, state := range states {
		events := d.Process(Input{CH: state, HW: false, Time: now.Add(time.Duration(i*50) * time.Millisecond)})
		if len(events) != 0 {
			t.Errorf("iteration %d: expected no events during bouncing, got %d", i, len(events))
		}
	}

	// Finally settle on ON
	events := d.Process(Input{CH: true, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events (debounce timer reset), got %d", len(events))
	}

	// Wait for debounce from last change
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(450 * time.Millisecond)})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after settling, got %d", len(events))
	}

	if events[0].Type != EventCHOn {
		t.Errorf("expected CH_ON, got %s", events[0].Type)
	}
}

func TestBackToBackTransitions(t *testing.T) {
	d := setupBaselinedDetector(t, false, false) // CH=OFF, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// First transition: CH OFF -> ON
	d.Process(Input{CH: true, HW: false, Time: now})
	events := d.Process(Input{CH: true, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 || events[0].Type != EventCHOn {
		t.Fatalf("expected CH_ON event, got %v", events)
	}

	// Second transition: CH ON -> OFF (starts immediately after first)
	t2 := now.Add(300 * time.Millisecond)
	d.Process(Input{CH: false, HW: false, Time: t2})
	events = d.Process(Input{CH: false, HW: false, Time: t2.Add(250 * time.Millisecond)})
	if len(events) != 1 || events[0].Type != EventCHOff {
		t.Fatalf("expected CH_OFF event, got %v", events)
	}
}

func TestSimultaneousTransitions(t *testing.T) {
	d := setupBaselinedDetector(t, false, false) // CH=OFF, HW=OFF
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// Both channels change at once
	d.Process(Input{CH: true, HW: true, Time: now})
	events := d.Process(Input{CH: true, HW: true, Time: now.Add(250 * time.Millisecond)})

	if len(events) != 2 {
		t.Fatalf("expected 2 events for simultaneous transitions, got %d", len(events))
	}

	// CH event should come first
	if events[0].Type != EventCHOn {
		t.Errorf("expected first event to be CH_ON, got %s", events[0].Type)
	}
	if events[1].Type != EventHWOn {
		t.Errorf("expected second event to be HW_ON, got %s", events[1].Type)
	}

	// Both events should reflect the new stable state
	for i, e := range events {
		if e.CHState != StateOn || e.HWState != StateOn {
			t.Errorf("event %d: expected both states ON, got CH=%s HW=%s", i, e.CHState, e.HWState)
		}
	}
}

func TestEventContainsCorrectStates(t *testing.T) {
	d := setupBaselinedDetector(t, true, true) // CH=ON, HW=ON
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// Only CH changes
	d.Process(Input{CH: false, HW: true, Time: now})
	events := d.Process(Input{CH: false, HW: true, Time: now.Add(250 * time.Millisecond)})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.CHState != StateOff {
		t.Errorf("expected CHState=OFF, got %s", e.CHState)
	}
	if e.HWState != StateOn {
		t.Errorf("expected HWState=ON (unchanged), got %s", e.HWState)
	}
}

func TestBoolToState(t *testing.T) {
	if boolToState(true) != StateOn {
		t.Error("boolToState(true) should be ON")
	}
	if boolToState(false) != StateOff {
		t.Error("boolToState(false) should be OFF")
	}
}

func TestEventTypeForTransition(t *testing.T) {
	tests := []struct {
		from  State
		to    State
		isCH  bool
		want  EventType
	}{
		{StateOff, StateOn, true, EventCHOn},
		{StateOn, StateOff, true, EventCHOff},
		{StateOff, StateOn, false, EventHWOn},
		{StateOn, StateOff, false, EventHWOff},
	}

	for _, tt := range tests {
		got := eventTypeForTransition(tt.from, tt.to, tt.isCH)
		if got == nil {
			t.Errorf("eventTypeForTransition(%s, %s, %v) returned nil", tt.from, tt.to, tt.isCH)
			continue
		}
		if *got != tt.want {
			t.Errorf("eventTypeForTransition(%s, %s, %v) = %s, want %s", tt.from, tt.to, tt.isCH, *got, tt.want)
		}
	}
}

func TestCurrentStateBeforeBaseline(t *testing.T) {
	d := NewDetector(250*time.Millisecond, time.Now())
	ch, hw := d.CurrentState()
	// Before baseline, states should be zero values
	if ch != "" {
		t.Errorf("expected empty CH state before baseline, got %s", ch)
	}
	if hw != "" {
		t.Errorf("expected empty HW state before baseline, got %s", hw)
	}
}

func TestAsymmetricBaseline(t *testing.T) {
	// Test where one channel baselines before the other
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, now)

	// Both start
	d.Process(Input{CH: true, HW: false, Time: now})

	// HW changes but CH stays stable
	d.Process(Input{CH: true, HW: true, Time: now.Add(100 * time.Millisecond)})

	// At 250ms: CH should baseline, HW timer reset
	events := d.Process(Input{CH: true, HW: true, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events, got %d", len(events))
	}
	if d.IsBaselined() {
		t.Error("should not be fully baselined yet (HW timer was reset)")
	}

	// At 350ms: HW should baseline (250ms after its change at 100ms)
	events = d.Process(Input{CH: true, HW: true, Time: now.Add(350 * time.Millisecond)})
	if len(events) != 0 {
		t.Errorf("expected no events at baseline, got %d", len(events))
	}
	if !d.IsBaselined() {
		t.Error("should be fully baselined now")
	}
}

func TestDebounceExactTiming(t *testing.T) {
	d := setupBaselinedDetector(t, false, false)
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)

	// Start transition
	d.Process(Input{CH: true, HW: false, Time: now})

	// Just before debounce (249ms)
	events := d.Process(Input{CH: true, HW: false, Time: now.Add(249 * time.Millisecond)})
	if len(events) != 0 {
		t.Error("should not trigger at 249ms")
	}

	// Exactly at debounce (250ms)
	events = d.Process(Input{CH: true, HW: false, Time: now.Add(250 * time.Millisecond)})
	if len(events) != 1 {
		t.Error("should trigger at exactly 250ms")
	}
}

// setupBaselinedDetector creates a detector that has already established baseline.
func setupBaselinedDetector(t *testing.T, chOn, hwOn bool) *Detector {
	t.Helper()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, now)

	// Establish baseline
	d.Process(Input{CH: chOn, HW: hwOn, Time: now})
	d.Process(Input{CH: chOn, HW: hwOn, Time: now.Add(250 * time.Millisecond)})

	if !d.IsBaselined() {
		t.Fatal("failed to establish baseline")
	}

	return d
}

// Heartbeat tests

func TestEventCountsIncrementOnTransition(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline with both OFF
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Verify initial counts are zero
	if d.eventCounts.CHOn != 0 || d.eventCounts.CHOff != 0 || d.eventCounts.HWOn != 0 || d.eventCounts.HWOff != 0 {
		t.Error("event counts should be zero after baseline")
	}

	// CH turns ON
	t1 := startTime.Add(500 * time.Millisecond)
	d.Process(Input{CH: true, HW: false, Time: t1})
	d.Process(Input{CH: true, HW: false, Time: t1.Add(250 * time.Millisecond)})
	if d.eventCounts.CHOn != 1 {
		t.Errorf("expected CHOn=1, got %d", d.eventCounts.CHOn)
	}

	// HW turns ON
	t2 := t1.Add(500 * time.Millisecond)
	d.Process(Input{CH: true, HW: true, Time: t2})
	d.Process(Input{CH: true, HW: true, Time: t2.Add(250 * time.Millisecond)})
	if d.eventCounts.HWOn != 1 {
		t.Errorf("expected HWOn=1, got %d", d.eventCounts.HWOn)
	}

	// CH turns OFF
	t3 := t2.Add(500 * time.Millisecond)
	d.Process(Input{CH: false, HW: true, Time: t3})
	d.Process(Input{CH: false, HW: true, Time: t3.Add(250 * time.Millisecond)})
	if d.eventCounts.CHOff != 1 {
		t.Errorf("expected CHOff=1, got %d", d.eventCounts.CHOff)
	}

	// HW turns OFF
	t4 := t3.Add(500 * time.Millisecond)
	d.Process(Input{CH: false, HW: false, Time: t4})
	d.Process(Input{CH: false, HW: false, Time: t4.Add(250 * time.Millisecond)})
	if d.eventCounts.HWOff != 1 {
		t.Errorf("expected HWOff=1, got %d", d.eventCounts.HWOff)
	}

	// Verify all counts
	if d.eventCounts.CHOn != 1 || d.eventCounts.CHOff != 1 || d.eventCounts.HWOn != 1 || d.eventCounts.HWOff != 1 {
		t.Errorf("expected all counts to be 1, got CHOn=%d CHOff=%d HWOn=%d HWOff=%d",
			d.eventCounts.CHOn, d.eventCounts.CHOff, d.eventCounts.HWOn, d.eventCounts.HWOff)
	}
}

func TestCheckHeartbeatDisabledWithZeroInterval(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Should return nil with zero interval (disabled)
	hb := d.CheckHeartbeat(startTime.Add(15*time.Minute), 0)
	if hb != nil {
		t.Error("should not return heartbeat when interval is 0 (disabled)")
	}

	// Should also return nil with negative interval
	hb = d.CheckHeartbeat(startTime.Add(15*time.Minute), -1*time.Minute)
	if hb != nil {
		t.Error("should not return heartbeat when interval is negative")
	}
}

func TestCheckHeartbeatBeforeBaseline(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Not baselined yet
	d.Process(Input{CH: false, HW: false, Time: startTime})

	// Try to get heartbeat after interval
	hb := d.CheckHeartbeat(startTime.Add(15*time.Minute), 15*time.Minute)
	if hb != nil {
		t.Error("should not return heartbeat before baseline")
	}
}

func TestCheckHeartbeatBeforeInterval(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Check before interval elapsed
	hb := d.CheckHeartbeat(startTime.Add(14*time.Minute), 15*time.Minute)
	if hb != nil {
		t.Error("should not return heartbeat before interval")
	}
}

func TestCheckHeartbeatAtInterval(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Check at interval
	checkTime := startTime.Add(15 * time.Minute)
	hb := d.CheckHeartbeat(checkTime, 15*time.Minute)
	if hb == nil {
		t.Fatal("should return heartbeat at interval")
	}

	if !hb.Timestamp.Equal(checkTime) {
		t.Errorf("expected timestamp %v, got %v", checkTime, hb.Timestamp)
	}
}

func TestCheckHeartbeatUpdatesLastTime(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// First heartbeat
	t1 := startTime.Add(15 * time.Minute)
	hb1 := d.CheckHeartbeat(t1, 15*time.Minute)
	if hb1 == nil {
		t.Fatal("should return first heartbeat")
	}

	// Check immediately after - should return nil
	hb2 := d.CheckHeartbeat(t1.Add(time.Second), 15*time.Minute)
	if hb2 != nil {
		t.Error("should not return heartbeat immediately after previous")
	}

	// Second heartbeat after interval from first
	t2 := t1.Add(15 * time.Minute)
	hb3 := d.CheckHeartbeat(t2, 15*time.Minute)
	if hb3 == nil {
		t.Fatal("should return second heartbeat")
	}
}

func TestHeartbeatUptimeCalculation(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Check at 15 minutes
	checkTime := startTime.Add(15 * time.Minute)
	hb := d.CheckHeartbeat(checkTime, 15*time.Minute)
	if hb == nil {
		t.Fatal("should return heartbeat")
	}

	expectedUptime := 15 * time.Minute
	if hb.Uptime != expectedUptime {
		t.Errorf("expected uptime %v, got %v", expectedUptime, hb.Uptime)
	}
}

func TestHeartbeatContainsEventCounts(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline with both OFF
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// Generate some events
	t1 := startTime.Add(500 * time.Millisecond)
	d.Process(Input{CH: true, HW: false, Time: t1})
	d.Process(Input{CH: true, HW: false, Time: t1.Add(250 * time.Millisecond)}) // CH_ON

	t2 := t1.Add(500 * time.Millisecond)
	d.Process(Input{CH: true, HW: true, Time: t2})
	d.Process(Input{CH: true, HW: true, Time: t2.Add(250 * time.Millisecond)}) // HW_ON

	// Get heartbeat
	hb := d.CheckHeartbeat(startTime.Add(15*time.Minute), 15*time.Minute)
	if hb == nil {
		t.Fatal("should return heartbeat")
	}

	if hb.Counts.CHOn != 1 {
		t.Errorf("expected CHOn=1, got %d", hb.Counts.CHOn)
	}
	if hb.Counts.HWOn != 1 {
		t.Errorf("expected HWOn=1, got %d", hb.Counts.HWOn)
	}
	if hb.Counts.CHOff != 0 {
		t.Errorf("expected CHOff=0, got %d", hb.Counts.CHOff)
	}
	if hb.Counts.HWOff != 0 {
		t.Errorf("expected HWOff=0, got %d", hb.Counts.HWOff)
	}
}

func TestMultipleHeartbeatsAccumulateCounts(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := NewDetector(250*time.Millisecond, startTime)

	// Establish baseline
	d.Process(Input{CH: false, HW: false, Time: startTime})
	d.Process(Input{CH: false, HW: false, Time: startTime.Add(250 * time.Millisecond)})

	// First event
	t1 := startTime.Add(500 * time.Millisecond)
	d.Process(Input{CH: true, HW: false, Time: t1})
	d.Process(Input{CH: true, HW: false, Time: t1.Add(250 * time.Millisecond)}) // CH_ON

	// First heartbeat
	hb1 := d.CheckHeartbeat(startTime.Add(15*time.Minute), 15*time.Minute)
	if hb1.Counts.CHOn != 1 {
		t.Errorf("first heartbeat: expected CHOn=1, got %d", hb1.Counts.CHOn)
	}

	// Second event
	t2 := startTime.Add(16 * time.Minute)
	d.Process(Input{CH: false, HW: false, Time: t2})
	d.Process(Input{CH: false, HW: false, Time: t2.Add(250 * time.Millisecond)}) // CH_OFF

	// Second heartbeat - counts should include all events
	hb2 := d.CheckHeartbeat(startTime.Add(30*time.Minute), 15*time.Minute)
	if hb2.Counts.CHOn != 1 {
		t.Errorf("second heartbeat: expected CHOn=1, got %d", hb2.Counts.CHOn)
	}
	if hb2.Counts.CHOff != 1 {
		t.Errorf("second heartbeat: expected CHOff=1, got %d", hb2.Counts.CHOff)
	}
}
