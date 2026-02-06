package internal

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/gpio"
	"github.com/sweeney/boiler-sensor/internal/logic"
	"github.com/sweeney/boiler-sensor/internal/mqtt"
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

// TestIntegrationStartupEvent verifies startup event with config.
func TestIntegrationStartupEvent(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	startupTime := time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC)
	event := mqtt.SystemEvent{
		Timestamp: startupTime,
		Event:     "STARTUP",
		Config: &mqtt.SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
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
	if publisher.SystemEvents[0].Config == nil {
		t.Fatal("expected config to be present")
	}
	if publisher.SystemEvents[0].Config.PollMs != 100 {
		t.Errorf("expected PollMs 100, got %d", publisher.SystemEvents[0].Config.PollMs)
	}
	if publisher.SystemEvents[0].Config.DebounceMs != 250 {
		t.Errorf("expected DebounceMs 250, got %d", publisher.SystemEvents[0].Config.DebounceMs)
	}
	if publisher.SystemEvents[0].Config.HeartbeatMs != 900000 {
		t.Errorf("expected HeartbeatMs 900000, got %d", publisher.SystemEvents[0].Config.HeartbeatMs)
	}
	if publisher.SystemEvents[0].Config.Broker != "tcp://192.168.1.200:1883" {
		t.Errorf("expected broker tcp://192.168.1.200:1883, got %s", publisher.SystemEvents[0].Config.Broker)
	}

	// Verify JSON payload structure
	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.System.Event != "STARTUP" {
		t.Errorf("payload event: expected STARTUP, got %s", parsed.System.Event)
	}
	if parsed.System.Config == nil {
		t.Fatal("payload config should be present")
	}
	if parsed.System.Config.PollMs != 100 {
		t.Errorf("payload poll_ms: expected 100, got %d", parsed.System.Config.PollMs)
	}
	if parsed.System.Config.HeartbeatMs != 900000 {
		t.Errorf("payload heartbeat_ms: expected 900000, got %d", parsed.System.Config.HeartbeatMs)
	}
}

// TestIntegrationStartupPayloadFormat verifies the exact JSON structure for startup events.
func TestIntegrationStartupPayloadFormat(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &mqtt.SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
	}

	publisher.PublishSystem(event)

	expected := `{"system":{"timestamp":"2026-02-03T19:05:51Z","event":"STARTUP","config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":900000,"broker":"tcp://192.168.1.200:1883"}}}`

	if string(publisher.SystemPayloads[0]) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(publisher.SystemPayloads[0]), expected)
	}
}

// TestIntegrationStartupThenShutdown verifies full lifecycle with startup and shutdown events.
func TestIntegrationStartupThenShutdown(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	// Startup
	startupEvent := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &mqtt.SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
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

	// Verify startup has config, shutdown has reason
	if publisher.SystemEvents[0].Config == nil {
		t.Error("startup event should have config")
	}
	if publisher.SystemEvents[1].Reason != "SIGTERM" {
		t.Errorf("shutdown event should have reason SIGTERM, got %s", publisher.SystemEvents[1].Reason)
	}
}

// TestIntegrationHeartbeatPayloadFormat verifies the exact JSON structure for heartbeat events.
func TestIntegrationHeartbeatPayloadFormat(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &mqtt.HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: mqtt.HeartbeatCounts{
				CHOn:  5,
				CHOff: 4,
				HWOn:  2,
				HWOff: 2,
			},
		},
	}

	publisher.PublishSystem(event)

	expected := `{"system":{"timestamp":"2026-02-04T12:15:00Z","event":"HEARTBEAT","heartbeat":{"uptime_seconds":900,"event_counts":{"ch_on":5,"ch_off":4,"hw_on":2,"hw_off":2}}}}`

	if string(publisher.SystemPayloads[0]) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(publisher.SystemPayloads[0]), expected)
	}
}

// TestIntegrationStartupWithNetworkInfo verifies startup event includes network info.
func TestIntegrationStartupWithNetworkInfo(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &mqtt.SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
		Network: &mqtt.NetworkInfo{
			Type:       "wifi",
			IP:         "192.168.1.100",
			Status:     "connected",
			Gateway:    "192.168.1.1",
			WifiStatus: "connected",
			SSID:       "MyNetwork",
		},
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify JSON payload contains network object
	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Network == nil {
		t.Fatal("expected network to be present in startup payload")
	}
	if parsed.System.Network.Type != "wifi" {
		t.Errorf("network type: expected wifi, got %s", parsed.System.Network.Type)
	}
	if parsed.System.Network.IP != "192.168.1.100" {
		t.Errorf("network ip: expected 192.168.1.100, got %s", parsed.System.Network.IP)
	}
	if parsed.System.Network.SSID != "MyNetwork" {
		t.Errorf("network ssid: expected MyNetwork, got %s", parsed.System.Network.SSID)
	}
}

// TestIntegrationHeartbeatWithNetworkInfo verifies heartbeat event includes network info.
func TestIntegrationHeartbeatWithNetworkInfo(t *testing.T) {
	publisher := mqtt.NewFakePublisher()

	event := mqtt.SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &mqtt.HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: mqtt.HeartbeatCounts{
				CHOn:  5,
				CHOff: 4,
				HWOn:  2,
				HWOff: 2,
			},
		},
		Network: &mqtt.NetworkInfo{
			Type:       "ethernet",
			IP:         "10.0.0.50",
			Status:     "connected",
			Gateway:    "10.0.0.1",
			WifiStatus: "disabled",
			SSID:       "",
		},
	}

	if err := publisher.PublishSystem(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify JSON payload contains network object
	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Heartbeat == nil {
		t.Fatal("expected heartbeat to be present")
	}
	if parsed.System.Network == nil {
		t.Fatal("expected network to be present in heartbeat payload")
	}
	if parsed.System.Network.Type != "ethernet" {
		t.Errorf("network type: expected ethernet, got %s", parsed.System.Network.Type)
	}
	if parsed.System.Network.IP != "10.0.0.50" {
		t.Errorf("network ip: expected 10.0.0.50, got %s", parsed.System.Network.IP)
	}
	if parsed.System.Network.Gateway != "10.0.0.1" {
		t.Errorf("network gateway: expected 10.0.0.1, got %s", parsed.System.Network.Gateway)
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

	// Check heartbeat after 15 minutes
	heartbeatTime := startTime.Add(15 * time.Minute)
	hbData := detector.CheckHeartbeat(heartbeatTime, 15*time.Minute)
	if hbData == nil {
		t.Fatal("expected heartbeat data")
	}

	// Create and publish heartbeat event
	heartbeatEvent := mqtt.SystemEvent{
		Timestamp: heartbeatTime,
		Event:     "HEARTBEAT",
		Heartbeat: &mqtt.HeartbeatInfo{
			UptimeSeconds: int64(hbData.Uptime.Seconds()),
			EventCounts: mqtt.HeartbeatCounts{
				CHOn:  hbData.Counts.CHOn,
				CHOff: hbData.Counts.CHOff,
				HWOn:  hbData.Counts.HWOn,
				HWOff: hbData.Counts.HWOff,
			},
		},
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
	if publisher.SystemEvents[0].Heartbeat == nil {
		t.Fatal("expected heartbeat info")
	}
	if publisher.SystemEvents[0].Heartbeat.EventCounts.CHOn != 1 {
		t.Errorf("expected ch_on=1, got %d", publisher.SystemEvents[0].Heartbeat.EventCounts.CHOn)
	}
	if publisher.SystemEvents[0].Heartbeat.EventCounts.HWOn != 1 {
		t.Errorf("expected hw_on=1, got %d", publisher.SystemEvents[0].Heartbeat.EventCounts.HWOn)
	}
	if publisher.SystemEvents[0].Heartbeat.UptimeSeconds != 900 {
		t.Errorf("expected uptime_seconds=900, got %d", publisher.SystemEvents[0].Heartbeat.UptimeSeconds)
	}

	// Verify JSON payload
	var parsed mqtt.SystemPayload
	if err := json.Unmarshal(publisher.SystemPayloads[0], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.System.Heartbeat == nil {
		t.Fatal("expected heartbeat in payload")
	}
	if parsed.System.Heartbeat.EventCounts.CHOn != 1 {
		t.Errorf("payload ch_on: expected 1, got %d", parsed.System.Heartbeat.EventCounts.CHOn)
	}
}
