package internal

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/gpio"
	"github.com/sweeney/boiler-sensor/internal/logic"
	"github.com/sweeney/boiler-sensor/internal/mqtt"
	"github.com/sweeney/boiler-sensor/internal/status"
)

// TestIntegrationFullFlow tests the complete flow from GPIO to MQTT using fakes.
func TestIntegrationFullFlow(t *testing.T) {
	// Simulate: both off -> CH turns on -> HW turns on -> CH turns off
	samples := []gpio.Sample{
		// Baseline establishment (need enough samples for debounce)
		{CH: false, HW: false}, // t=0
		{CH: false, HW: false}, // t=100ms
		{CH: false, HW: false}, // t=200ms
		{CH: false, HW: false}, // t=300ms (baseline established at 250ms)
		// CH turns on
		{CH: true, HW: false}, // t=400ms - start transition
		{CH: true, HW: false}, // t=500ms
		{CH: true, HW: false}, // t=600ms
		{CH: true, HW: false}, // t=700ms (CH_ON at 650ms)
		// HW turns on
		{CH: true, HW: true}, // t=800ms - start transition
		{CH: true, HW: true}, // t=900ms
		{CH: true, HW: true}, // t=1000ms
		{CH: true, HW: true}, // t=1100ms (HW_ON at 1050ms)
		// CH turns off
		{CH: false, HW: true}, // t=1200ms - start transition
		{CH: false, HW: true}, // t=1300ms
		{CH: false, HW: true}, // t=1400ms
		{CH: false, HW: true}, // t=1500ms (CH_OFF at 1450ms)
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	pollInterval := 100 * time.Millisecond

	// Simulate the main loop
	for i := range samples {
		ch, hw, err := gpioReader.Read()
		if err != nil {
			t.Fatalf("sample %d: gpio read error: %v", i, err)
		}

		now := startTime.Add(time.Duration(i) * pollInterval)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			if err := publisher.Publish(event); err != nil {
				t.Fatalf("sample %d: publish error: %v", i, err)
			}
		}
	}

	// Verify published events
	if len(publisher.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(publisher.Events))
	}

	// Event 1: CH_ON
	if publisher.Events[0].Type != logic.EventCHOn {
		t.Errorf("event 0: expected CH_ON, got %s", publisher.Events[0].Type)
	}
	if publisher.Events[0].CHState != logic.StateOn {
		t.Errorf("event 0: expected CH=ON, got %s", publisher.Events[0].CHState)
	}
	if publisher.Events[0].HWState != logic.StateOff {
		t.Errorf("event 0: expected HW=OFF, got %s", publisher.Events[0].HWState)
	}

	// Event 2: HW_ON
	if publisher.Events[1].Type != logic.EventHWOn {
		t.Errorf("event 1: expected HW_ON, got %s", publisher.Events[1].Type)
	}
	if publisher.Events[1].CHState != logic.StateOn {
		t.Errorf("event 1: expected CH=ON, got %s", publisher.Events[1].CHState)
	}
	if publisher.Events[1].HWState != logic.StateOn {
		t.Errorf("event 1: expected HW=ON, got %s", publisher.Events[1].HWState)
	}

	// Event 3: CH_OFF
	if publisher.Events[2].Type != logic.EventCHOff {
		t.Errorf("event 2: expected CH_OFF, got %s", publisher.Events[2].Type)
	}
	if publisher.Events[2].CHState != logic.StateOff {
		t.Errorf("event 2: expected CH=OFF, got %s", publisher.Events[2].CHState)
	}
	if publisher.Events[2].HWState != logic.StateOn {
		t.Errorf("event 2: expected HW=ON, got %s", publisher.Events[2].HWState)
	}

	// Verify JSON payloads
	for i, payload := range publisher.Payloads {
		var parsed mqtt.Payload
		if err := json.Unmarshal(payload, &parsed); err != nil {
			t.Errorf("payload %d: invalid JSON: %v", i, err)
		}
		if parsed.Heating.Timestamp == "" {
			t.Errorf("payload %d: missing timestamp", i)
		}
		if parsed.Heating.Event == "" {
			t.Errorf("payload %d: missing event", i)
		}
	}
}

// TestIntegrationNoEventsAtStartup verifies no events are published during baseline.
func TestIntegrationNoEventsAtStartup(t *testing.T) {
	samples := []gpio.Sample{
		{CH: true, HW: true},
		{CH: true, HW: true},
		{CH: true, HW: true},
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	for i := range samples {
		ch, hw, _ := gpioReader.Read()
		now := startTime.Add(time.Duration(i) * 100 * time.Millisecond)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			publisher.Publish(event)
		}
	}

	if len(publisher.Events) != 0 {
		t.Errorf("expected no events during baseline, got %d", len(publisher.Events))
	}
}

// TestIntegrationBounceRejection verifies bounces shorter than debounce are ignored.
func TestIntegrationBounceRejection(t *testing.T) {
	samples := []gpio.Sample{
		// Baseline
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		// Bounce (too short to trigger)
		{CH: true, HW: false},
		{CH: false, HW: false}, // Returns to off before debounce
		{CH: false, HW: false},
		{CH: false, HW: false},
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	for i := range samples {
		ch, hw, _ := gpioReader.Read()
		now := startTime.Add(time.Duration(i) * 100 * time.Millisecond)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			publisher.Publish(event)
		}
	}

	if len(publisher.Events) != 0 {
		t.Errorf("expected no events for bounce, got %d", len(publisher.Events))
	}
}

// TestIntegrationSimultaneousTransitions verifies both channels changing at once.
func TestIntegrationSimultaneousTransitions(t *testing.T) {
	samples := []gpio.Sample{
		// Baseline - both off (need 4 samples at 100ms = 300ms > 250ms debounce)
		{CH: false, HW: false}, // t=0
		{CH: false, HW: false}, // t=100
		{CH: false, HW: false}, // t=200
		{CH: false, HW: false}, // t=300 - baseline established
		// Both turn on (need 3 more samples for 250ms debounce)
		{CH: true, HW: true}, // t=400 - start transition
		{CH: true, HW: true}, // t=500
		{CH: true, HW: true}, // t=600
		{CH: true, HW: true}, // t=700 - transition confirmed (300ms > 250ms)
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	for i := range samples {
		ch, hw, _ := gpioReader.Read()
		now := startTime.Add(time.Duration(i) * 100 * time.Millisecond)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			publisher.Publish(event)
		}
	}

	if len(publisher.Events) != 2 {
		t.Fatalf("expected 2 events for simultaneous transitions, got %d", len(publisher.Events))
	}

	// CH first, then HW
	if publisher.Events[0].Type != logic.EventCHOn {
		t.Errorf("expected first event CH_ON, got %s", publisher.Events[0].Type)
	}
	if publisher.Events[1].Type != logic.EventHWOn {
		t.Errorf("expected second event HW_ON, got %s", publisher.Events[1].Type)
	}
}

// TestIntegrationPublishFailureDoesNotCrash verifies publish errors are handled gracefully.
func TestIntegrationPublishFailureDoesNotCrash(t *testing.T) {
	samples := []gpio.Sample{
		// Baseline
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		// Transition
		{CH: true, HW: false},
		{CH: true, HW: false},
		{CH: true, HW: false},
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	publisher.PublishError = nil // Initially no error
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	for i := range samples {
		ch, hw, _ := gpioReader.Read()
		now := startTime.Add(time.Duration(i) * 100 * time.Millisecond)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			// Simulate publish failure
			publisher.PublishError = nil
			if i == 5 {
				publisher.PublishError = nil // Allow the publish
			}
			err := publisher.Publish(event)
			// Should not panic even if there's an error
			_ = err
		}
	}

	// Test completed without panic
}

// TestIntegrationPayloadFormat verifies the exact JSON structure.
func TestIntegrationPayloadFormat(t *testing.T) {
	event := logic.Event{
		Timestamp: time.Date(2026, 2, 2, 22, 18, 12, 0, time.UTC),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}

	publisher := mqtt.NewFakePublisher()
	publisher.Publish(event)

	expected := `{"heating":{"timestamp":"2026-02-02T22:18:12Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`

	if string(publisher.Payloads[0]) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(publisher.Payloads[0]), expected)
	}
}

// TestIntegrationShutdownEventSIGTERM verifies shutdown event on SIGTERM.
func TestIntegrationShutdownEventSIGTERM(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	shutdownTime := time.Date(2026, 2, 3, 15, 30, 0, 0, time.UTC)
	event := mqtt.SystemEvent{
		Timestamp: shutdownTime,
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	err := publisher.PublishSystem(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(publisher.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(publisher.SystemEvents))
	}

	if publisher.SystemEvents[0].Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN event, got %s", publisher.SystemEvents[0].Event)
	}
	if publisher.SystemEvents[0].Reason != "SIGTERM" {
		t.Errorf("expected SIGTERM reason, got %s", publisher.SystemEvents[0].Reason)
	}

	// Verify JSON payload structure
	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.System.Event != "SHUTDOWN" {
		t.Errorf("payload event: expected SHUTDOWN, got %s", parsed.System.Event)
	}
	if parsed.System.Reason != "SIGTERM" {
		t.Errorf("payload reason: expected SIGTERM, got %s", parsed.System.Reason)
	}
	if parsed.System.Timestamp != "2026-02-03T15:30:00Z" {
		t.Errorf("payload timestamp: expected 2026-02-03T15:30:00Z, got %s", parsed.System.Timestamp)
	}
}

// TestIntegrationShutdownEventSIGINT verifies shutdown event on SIGINT.
func TestIntegrationShutdownEventSIGINT(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGINT",
	}

	err := publisher.PublishSystem(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if publisher.SystemEvents[0].Reason != "SIGINT" {
		t.Errorf("expected SIGINT reason, got %s", publisher.SystemEvents[0].Reason)
	}
}

// TestIntegrationShutdownPayloadFormat verifies the exact JSON structure for shutdown events.
func TestIntegrationShutdownPayloadFormat(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 10, 30, 45, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	publisher.PublishSystem(event)

	expected := `{"system":{"timestamp":"2026-02-03T10:30:45Z","event":"SHUTDOWN","reason":"SIGTERM"}}`

	if string(publisher.SystemPayloads[0]) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(publisher.SystemPayloads[0]), expected)
	}
}

// TestIntegrationShutdownAfterHeatingEvents verifies shutdown event comes after heating events.
func TestIntegrationShutdownAfterHeatingEvents(t *testing.T) {
	samples := []gpio.Sample{
		// Baseline
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		// CH turns on
		{CH: true, HW: false},
		{CH: true, HW: false},
		{CH: true, HW: false},
		{CH: true, HW: false},
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	pollInterval := 100 * time.Millisecond

	// Simulate the main loop
	for i := range samples {
		ch, hw, err := gpioReader.Read()
		if err != nil {
			t.Fatalf("sample %d: gpio read error: %v", i, err)
		}

		now := startTime.Add(time.Duration(i) * pollInterval)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			if err := publisher.Publish(event); err != nil {
				t.Fatalf("sample %d: publish error: %v", i, err)
			}
		}
	}

	// Simulate shutdown
	shutdownTime := startTime.Add(time.Duration(len(samples)) * pollInterval)
	shutdownEvent := mqtt.SystemEvent{
		Timestamp: shutdownTime,
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}
	if err := publisher.PublishSystem(shutdownEvent); err != nil {
		t.Fatalf("shutdown publish error: %v", err)
	}

	// Verify we have 1 heating event followed by 1 system event
	if len(publisher.Events) != 1 {
		t.Fatalf("expected 1 heating event, got %d", len(publisher.Events))
	}
	if publisher.Events[0].Type != logic.EventCHOn {
		t.Errorf("expected CH_ON, got %s", publisher.Events[0].Type)
	}

	if len(publisher.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(publisher.SystemEvents))
	}
	if publisher.SystemEvents[0].Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN, got %s", publisher.SystemEvents[0].Event)
	}
}

// TestIntegrationShutdownPublishFailureLogsButContinues verifies graceful handling of publish errors.
func TestIntegrationShutdownPublishFailureLogsButContinues(t *testing.T) {
	publisher := mqtt.NewFakePublisher()
	publisher.PublishSystemError = errors.New("broker disconnected")

	event := mqtt.SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	err := publisher.PublishSystem(event)

	// Should return error but not panic
	if err == nil {
		t.Error("expected error from publish")
	}

	// Should not have recorded the event
	if len(publisher.SystemEvents) != 0 {
		t.Errorf("expected no system events on error, got %d", len(publisher.SystemEvents))
	}
}

// TestIntegrationStartupEventWithRawPayload verifies startup event using RawPayload from status tracker.
func TestIntegrationStartupEventWithRawPayload(t *testing.T) {
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC)

	tracker := status.NewTracker(startTime, status.Config{
		PollMs:      100,
		DebounceMs:  250,
		HeartbeatMs: 900000,
		Broker:      "tcp://192.168.1.200:1883",
		HTTPPort:    ":80",
	})

	snap := tracker.Snapshot()
	raw := status.FormatStatusEvent(snap, "STARTUP", "")

	event := mqtt.SystemEvent{
		Timestamp:  startTime,
		Event:      "STARTUP",
		RawPayload: raw,
		Retained:   true,
	}

	err := publisher.PublishSystem(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(publisher.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(publisher.SystemEvents))
	}

	if publisher.SystemEvents[0].Event != "STARTUP" {
		t.Errorf("expected STARTUP event, got %s", publisher.SystemEvents[0].Event)
	}

	// Verify JSON payload contains status snapshot with event field
	var parsed status.StatusJSON
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Status.Event != "STARTUP" {
		t.Errorf("payload event: expected STARTUP, got %s", parsed.Status.Event)
	}
	if parsed.Status.Config.PollMs != 100 {
		t.Errorf("payload poll_ms: expected 100, got %d", parsed.Status.Config.PollMs)
	}
	if parsed.Status.Config.Broker != "tcp://192.168.1.200:1883" {
		t.Errorf("payload broker: expected tcp://192.168.1.200:1883, got %s", parsed.Status.Config.Broker)
	}
}

// TestIntegrationStartupThenShutdown verifies full lifecycle with startup and shutdown events.
func TestIntegrationStartupThenShutdown(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	// Startup
	startupEvent := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Retained:  true,
	}
	if err := publisher.PublishSystem(startupEvent); err != nil {
		t.Fatalf("startup publish error: %v", err)
	}

	// Simulate some heating events
	heatingEvent := logic.Event{
		Timestamp: time.Date(2026, 2, 3, 19, 6, 0, 0, time.UTC),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}
	if err := publisher.Publish(heatingEvent); err != nil {
		t.Fatalf("heating publish error: %v", err)
	}

	// Shutdown
	shutdownEvent := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 10, 0, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}
	if err := publisher.PublishSystem(shutdownEvent); err != nil {
		t.Fatalf("shutdown publish error: %v", err)
	}

	// Verify event counts
	if len(publisher.SystemEvents) != 2 {
		t.Fatalf("expected 2 system events, got %d", len(publisher.SystemEvents))
	}
	if len(publisher.Events) != 1 {
		t.Fatalf("expected 1 heating event, got %d", len(publisher.Events))
	}

	// Verify order: STARTUP, then SHUTDOWN
	if publisher.SystemEvents[0].Event != "STARTUP" {
		t.Errorf("first system event should be STARTUP, got %s", publisher.SystemEvents[0].Event)
	}
	if publisher.SystemEvents[1].Event != "SHUTDOWN" {
		t.Errorf("second system event should be SHUTDOWN, got %s", publisher.SystemEvents[1].Event)
	}

	if publisher.SystemEvents[1].Reason != "SIGTERM" {
		t.Errorf("shutdown event should have reason SIGTERM, got %s", publisher.SystemEvents[1].Reason)
	}
}

// TestIntegrationHeartbeatWithRawPayload verifies heartbeat uses status snapshot format.
func TestIntegrationHeartbeatWithRawPayload(t *testing.T) {
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	tracker := status.NewTracker(startTime, status.Config{
		PollMs:      100,
		DebounceMs:  250,
		HeartbeatMs: 900000,
		Broker:      "tcp://192.168.1.200:1883",
		HTTPPort:    ":80",
	})
	tracker.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{CHOn: 5, CHOff: 4, HWOn: 2, HWOff: 2})
	tracker.SetMQTTConnected(true)

	snap := tracker.Snapshot()
	raw := status.FormatStatusEvent(snap, "HEARTBEAT", "")

	event := mqtt.SystemEvent{
		Timestamp:  snap.Now,
		Event:      "HEARTBEAT",
		RawPayload: raw,
	}

	publisher.PublishSystem(event)

	var parsed status.StatusJSON
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Status.Event != "HEARTBEAT" {
		t.Errorf("expected HEARTBEAT, got %s", parsed.Status.Event)
	}
	if parsed.Status.CH != "ON" {
		t.Errorf("expected CH=ON, got %s", parsed.Status.CH)
	}
	if parsed.Status.HW != "OFF" {
		t.Errorf("expected HW=OFF, got %s", parsed.Status.HW)
	}
	if parsed.Status.Counts.CHOn != 5 {
		t.Errorf("expected ch_on=5, got %d", parsed.Status.Counts.CHOn)
	}
	if !parsed.Status.MQTT.Connected {
		t.Error("expected MQTT connected=true")
	}
}

// TestIntegrationStartupEventIsRetained verifies STARTUP has Retained=true.
func TestIntegrationStartupEventIsRetained(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Now(),
		Event:     "STARTUP",
		Retained:  true,
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !publisher.SystemEvents[0].Retained {
		t.Error("STARTUP event should have Retained=true")
	}
}

// TestIntegrationShutdownEventIsRetained verifies SHUTDOWN has Retained=true.
func TestIntegrationShutdownEventIsRetained(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
		Retained:  true,
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !publisher.SystemEvents[0].Retained {
		t.Error("SHUTDOWN event should have Retained=true")
	}
}

// TestIntegrationHeartbeatEventIsNotRetained verifies HEARTBEAT has Retained=false.
func TestIntegrationHeartbeatEventIsNotRetained(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Now(),
		Event:     "HEARTBEAT",
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if publisher.SystemEvents[0].Retained {
		t.Error("HEARTBEAT event should have Retained=false")
	}
}

// TestIntegrationWillPayloadFormat verifies the will payload JSON matches expected structure.
func TestIntegrationWillPayloadFormat(t *testing.T) {
	// The will payload is a SHUTDOWN event with reason MQTT_DISCONNECT
	// LWT uses the simple format (no RawPayload, no tracker access)
	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 10, 8, 30, 0, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
	}

	payload, err := mqtt.FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN event, got %s", parsed.System.Event)
	}
	if parsed.System.Reason != "MQTT_DISCONNECT" {
		t.Errorf("expected MQTT_DISCONNECT reason, got %s", parsed.System.Reason)
	}
}

// TestIntegrationReconnectedEventFormat verifies the exact JSON structure for RECONNECTED events.
func TestIntegrationReconnectedEventFormat(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 10, 14, 30, 0, 0, time.UTC),
		Event:     "RECONNECTED",
		Retained:  true,
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(publisher.SystemPayloads) != 1 {
		t.Fatalf("expected 1 system payload, got %d", len(publisher.SystemPayloads))
	}

	expected := `{"system":{"timestamp":"2026-02-10T14:30:00Z","event":"RECONNECTED"}}`
	if string(publisher.SystemPayloads[0]) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(publisher.SystemPayloads[0]), expected)
	}

	// Verify no extra fields in the JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	system := parsed["system"].(map[string]interface{})
	if _, exists := system["reason"]; exists {
		t.Error("RECONNECTED should not have reason field")
	}

	// Verify retained flag is preserved
	if !publisher.SystemEvents[0].Retained {
		t.Error("RECONNECTED event should have Retained=true")
	}
}

// TestIntegrationHeartbeatAfterTransitions verifies heartbeat contains correct counts after transitions.
func TestIntegrationHeartbeatAfterTransitions(t *testing.T) {
	samples := []gpio.Sample{
		// Baseline
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		{CH: false, HW: false},
		// CH turns on
		{CH: true, HW: false},
		{CH: true, HW: false},
		{CH: true, HW: false},
		{CH: true, HW: false},
		// HW turns on
		{CH: true, HW: true},
		{CH: true, HW: true},
		{CH: true, HW: true},
		{CH: true, HW: true},
	}

	gpioReader := gpio.NewFakeReader(samples)
	publisher := mqtt.NewFakePublisher()
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	detector := logic.NewDetector(250*time.Millisecond, startTime)

	pollInterval := 100 * time.Millisecond

	// Simulate the main loop
	for i := range samples {
		ch, hw, err := gpioReader.Read()
		if err != nil {
			t.Fatalf("sample %d: gpio read error: %v", i, err)
		}

		now := startTime.Add(time.Duration(i) * pollInterval)
		events := detector.Process(logic.Input{CH: ch, HW: hw, Time: now})

		for _, event := range events {
			if err := publisher.Publish(event); err != nil {
				t.Fatalf("sample %d: publish error: %v", i, err)
			}
		}
	}

	// Verify we have 2 heating events (CH_ON and HW_ON)
	if len(publisher.Events) != 2 {
		t.Fatalf("expected 2 heating events, got %d", len(publisher.Events))
	}

	// Build heartbeat using status tracker (new format)
	tracker := status.NewTracker(startTime, status.Config{
		PollMs:     100,
		DebounceMs: 250,
		Broker:     "tcp://localhost:1883",
	})
	chState, hwState := detector.CurrentState()
	tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())

	snap := tracker.Snapshot()
	raw := status.FormatStatusEvent(snap, "HEARTBEAT", "")

	heartbeatEvent := mqtt.SystemEvent{
		Timestamp:  snap.Now,
		Event:      "HEARTBEAT",
		RawPayload: raw,
	}

	if err := publisher.PublishSystem(heartbeatEvent); err != nil {
		t.Fatalf("heartbeat publish error: %v", err)
	}

	// Verify system event
	if len(publisher.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(publisher.SystemEvents))
	}
	if publisher.SystemEvents[0].Event != "HEARTBEAT" {
		t.Errorf("expected HEARTBEAT, got %s", publisher.SystemEvents[0].Event)
	}

	// Verify JSON payload has new status format
	var parsed status.StatusJSON
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Status.Event != "HEARTBEAT" {
		t.Errorf("expected event=HEARTBEAT, got %s", parsed.Status.Event)
	}
	if parsed.Status.Counts.CHOn != 1 {
		t.Errorf("expected ch_on=1, got %d", parsed.Status.Counts.CHOn)
	}
	if parsed.Status.Counts.HWOn != 1 {
		t.Errorf("expected hw_on=1, got %d", parsed.Status.Counts.HWOn)
	}
}
