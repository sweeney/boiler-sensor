package main

import (
	"testing"
	"time"

	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/status"
)

// TestWireContractCanonicalSequence pins the EXACT ordered bytes published to
// MQTT for a canonical GPIO scenario (both off -> CH on -> HW on -> CH off),
// followed by a heartbeat. This is the regression guard proving the imported
// generic engine (github.com/sweeney/common/iot), driven by boilerProfile(),
// reproduces the boiler's exact wire output. The expected byte literals are the
// contract — they are copied verbatim from the pre-migration oracle and MUST NOT
// change. If a change here is intentional, update the goldens deliberately.
func TestWireContractCanonicalSequence(t *testing.T) {
	prof := boilerProfile(defaultPinCH, defaultPinHW)

	// Logical (post-inversion) samples, [ch, hw], one per poll tick.
	samples := [][]bool{
		{false, false}, // t=0
		{false, false}, // t=100ms
		{false, false}, // t=200ms
		{false, false}, // t=300ms (baseline established)
		{true, false},  // t=400ms - CH transition starts
		{true, false},  // t=500ms
		{true, false},  // t=600ms
		{true, false},  // t=700ms - CH_ON confirmed
		{true, true},   // t=800ms - HW transition starts
		{true, true},   // t=900ms
		{true, true},   // t=1000ms
		{true, true},   // t=1100ms - HW_ON confirmed
		{false, true},  // t=1200ms - CH off transition starts
		{false, true},  // t=1300ms
		{false, true},  // t=1400ms
		{false, true},  // t=1500ms - CH_OFF confirmed
	}

	gpioReader := gpio.NewFakeChannelReader(samples)
	publisher := mqtt.NewFakePublisherForProfile(prof)
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewProfileDetector(prof, 250*time.Millisecond, startTime)
	keys := prof.Keys()
	pollInterval := 100 * time.Millisecond

	for i := range samples {
		vals, err := gpioReader.ReadChannels()
		if err != nil {
			t.Fatalf("sample %d: gpio read error: %v", i, err)
		}
		values := make(map[string]bool, len(keys))
		for j, k := range keys {
			values[k] = vals[j]
		}
		now := startTime.Add(time.Duration(i) * pollInterval)
		for _, event := range detector.ProcessValues(values, now) {
			if err := publisher.PublishEvent(event); err != nil {
				t.Fatalf("sample %d: publish error: %v", i, err)
			}
		}
	}

	// Event payloads (topic energy/boiler/sensor/events), in publish order.
	// These literals are the frozen boiler wire contract — DO NOT CHANGE.
	wantEvents := []string{
		`{"boiler":{"timestamp":"2026-01-01T12:00:00Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`,
		`{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`,
		`{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"CH_OFF","ch":{"state":"OFF"},"hw":{"state":"ON"}}}`,
	}
	if len(publisher.Payloads) != len(wantEvents) {
		t.Fatalf("expected %d event payloads, got %d", len(wantEvents), len(publisher.Payloads))
	}
	for i, want := range wantEvents {
		if got := string(publisher.Payloads[i]); got != want {
			t.Errorf("event payload %d:\n got:  %s\n want: %s", i, got, want)
		}
	}

	// Heartbeat payload (topic energy/boiler/sensor/system), built from the
	// status snapshot the way the daemon does.
	tracker := status.NewTrackerForProfile(prof, startTime, status.Config{
		PollMs:     100,
		DebounceMs: 250,
		Broker:     "tcp://192.168.1.200:1883",
	})
	tracker.UpdateChannels(detector.CurrentStates(), detector.IsBaselined(), detector.GenericCountsSnapshot())
	snap := tracker.Snapshot()
	snap.Now = startTime.Add(15 * time.Minute) // deterministic heartbeat timestamp
	raw := status.FormatStatusEvent(snap, "HEARTBEAT", "")

	// Frozen boiler wire contract — DO NOT CHANGE.
	wantHeartbeat := `{"status":{"event":"HEARTBEAT","ch":"OFF","hw":"ON","ready":true,"uptime_seconds":900,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:15:00Z","mqtt":{"connected":false,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":1,"ch_off":1,"hw_on":1,"hw_off":0},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":0,"broker":"tcp://192.168.1.200:1883","http_port":""}}}`
	if got := string(raw); got != wantHeartbeat {
		t.Errorf("heartbeat payload:\n got:  %s\n want: %s", got, wantHeartbeat)
	}
}
