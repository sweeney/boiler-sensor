package mqtt

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

func TestFormatPayload(t *testing.T) {
	event := logic.Event{
		Timestamp: time.Date(2026, 2, 2, 22, 18, 12, 0, time.UTC),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}

	payload, err := FormatPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed Payload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Heating.Timestamp != "2026-02-02T22:18:12Z" {
		t.Errorf("unexpected timestamp: %s", parsed.Heating.Timestamp)
	}
	if parsed.Heating.Event != "CH_ON" {
		t.Errorf("unexpected event: %s", parsed.Heating.Event)
	}
	if parsed.Heating.CH.State != "ON" {
		t.Errorf("unexpected CH state: %s", parsed.Heating.CH.State)
	}
	if parsed.Heating.HW.State != "OFF" {
		t.Errorf("unexpected HW state: %s", parsed.Heating.HW.State)
	}
}

func TestFormatPayloadAllEventTypes(t *testing.T) {
	tests := []struct {
		eventType logic.EventType
		chState   logic.State
		hwState   logic.State
		wantEvent string
		wantCH    string
		wantHW    string
	}{
		{logic.EventCHOn, logic.StateOn, logic.StateOff, "CH_ON", "ON", "OFF"},
		{logic.EventCHOff, logic.StateOff, logic.StateOn, "CH_OFF", "OFF", "ON"},
		{logic.EventHWOn, logic.StateOff, logic.StateOn, "HW_ON", "OFF", "ON"},
		{logic.EventHWOff, logic.StateOn, logic.StateOff, "HW_OFF", "ON", "OFF"},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			event := logic.Event{
				Timestamp: time.Now(),
				Type:      tt.eventType,
				CHState:   tt.chState,
				HWState:   tt.hwState,
			}

			payload, err := FormatPayload(event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var parsed Payload
			if err := json.Unmarshal(payload, &parsed); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			if parsed.Heating.Event != tt.wantEvent {
				t.Errorf("event: got %s, want %s", parsed.Heating.Event, tt.wantEvent)
			}
			if parsed.Heating.CH.State != tt.wantCH {
				t.Errorf("CH: got %s, want %s", parsed.Heating.CH.State, tt.wantCH)
			}
			if parsed.Heating.HW.State != tt.wantHW {
				t.Errorf("HW: got %s, want %s", parsed.Heating.HW.State, tt.wantHW)
			}
		})
	}
}

func TestFakePublisher(t *testing.T) {
	f := NewFakePublisher()

	event := logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}

	err := f.Publish(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(f.Events))
	}

	if f.Events[0].Type != logic.EventCHOn {
		t.Errorf("unexpected event type: %s", f.Events[0].Type)
	}

	if len(f.Payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(f.Payloads))
	}
}

func TestFakePublisherError(t *testing.T) {
	f := NewFakePublisher()
	f.PublishError = errors.New("simulated error")

	event := logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}

	err := f.Publish(event)
	if err == nil {
		t.Error("expected error")
	}

	if len(f.Events) != 0 {
		t.Errorf("expected no events recorded on error, got %d", len(f.Events))
	}
}

func TestFakePublisherClose(t *testing.T) {
	f := NewFakePublisher()

	if f.Closed {
		t.Error("should not be closed initially")
	}

	err := f.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !f.Closed {
		t.Error("should be closed after Close()")
	}
}

func TestFakePublisherReset(t *testing.T) {
	f := NewFakePublisher()

	event := logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}
	f.Publish(event)
	f.Close()
	f.PublishError = errors.New("error")

	f.Reset()

	if len(f.Events) != 0 {
		t.Error("events should be cleared")
	}
	if len(f.Payloads) != 0 {
		t.Error("payloads should be cleared")
	}
	if f.Closed {
		t.Error("closed should be reset")
	}
	if f.PublishError != nil {
		t.Error("error should be cleared")
	}
}

func TestTopic(t *testing.T) {
	expected := "energy/BOILER_SENSOR/SENSOR/heating"
	if Topic != expected {
		t.Errorf("unexpected topic: got %s, want %s", Topic, expected)
	}
}

func TestTopicSystem(t *testing.T) {
	expected := "energy/BOILER_SENSOR/SENSOR/system"
	if TopicSystem != expected {
		t.Errorf("unexpected system topic: got %s, want %s", TopicSystem, expected)
	}
}

func TestFormatSystemPayload(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 10, 30, 45, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Timestamp != "2026-02-03T10:30:45Z" {
		t.Errorf("unexpected timestamp: %s", parsed.System.Timestamp)
	}
	if parsed.System.Event != "SHUTDOWN" {
		t.Errorf("unexpected event: %s", parsed.System.Event)
	}
	if parsed.System.Reason != "SIGTERM" {
		t.Errorf("unexpected reason: %s", parsed.System.Reason)
	}
}

func TestFormatSystemPayloadExactJSON(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 10, 30, 45, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-03T10:30:45Z","event":"SHUTDOWN","reason":"SIGTERM"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadAllSignals(t *testing.T) {
	tests := []struct {
		reason     string
		wantReason string
	}{
		{"SIGTERM", "SIGTERM"},
		{"SIGINT", "SIGINT"},
		{"UNKNOWN", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			event := SystemEvent{
				Timestamp: time.Now(),
				Event:     "SHUTDOWN",
				Reason:    tt.reason,
			}

			payload, err := FormatSystemPayload(event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var parsed SystemPayload
			if err := json.Unmarshal(payload, &parsed); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			if parsed.System.Reason != tt.wantReason {
				t.Errorf("reason: got %s, want %s", parsed.System.Reason, tt.wantReason)
			}
		})
	}
}

func TestFormatSystemPayloadStartup(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Timestamp != "2026-02-03T19:05:51Z" {
		t.Errorf("unexpected timestamp: %s", parsed.System.Timestamp)
	}
	if parsed.System.Event != "STARTUP" {
		t.Errorf("unexpected event: %s", parsed.System.Event)
	}
	if parsed.System.Reason != "" {
		t.Errorf("expected empty reason for startup, got: %s", parsed.System.Reason)
	}
	if parsed.System.Config == nil {
		t.Fatal("expected config to be present")
	}
	if parsed.System.Config.PollMs != 100 {
		t.Errorf("unexpected poll_ms: %d", parsed.System.Config.PollMs)
	}
	if parsed.System.Config.DebounceMs != 250 {
		t.Errorf("unexpected debounce_ms: %d", parsed.System.Config.DebounceMs)
	}
	if parsed.System.Config.HeartbeatMs != 900000 {
		t.Errorf("unexpected heartbeat_ms: %d", parsed.System.Config.HeartbeatMs)
	}
	if parsed.System.Config.Broker != "tcp://192.168.1.200:1883" {
		t.Errorf("unexpected broker: %s", parsed.System.Config.Broker)
	}
}

func TestFormatSystemPayloadStartupExactJSON(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-03T19:05:51Z","event":"STARTUP","config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":900000,"broker":"tcp://192.168.1.200:1883"}}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadShutdownOmitsConfig(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 10, 0, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
		Config:    nil,
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Config should be omitted from JSON
	expected := `{"system":{"timestamp":"2026-02-03T19:10:00Z","event":"SHUTDOWN","reason":"SIGTERM"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadStartupOmitsReason(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Reason:    "", // Empty reason should be omitted
		Config: &SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reason should be omitted from JSON (no "reason":"")
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	system := parsed["system"].(map[string]interface{})
	if _, exists := system["reason"]; exists {
		t.Error("reason field should be omitted for startup events")
	}
}

func TestFakePublisherPublishSystem(t *testing.T) {
	f := NewFakePublisher()

	event := SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	err := f.PublishSystem(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(f.SystemEvents))
	}

	if f.SystemEvents[0].Event != "SHUTDOWN" {
		t.Errorf("unexpected event: %s", f.SystemEvents[0].Event)
	}
	if f.SystemEvents[0].Reason != "SIGTERM" {
		t.Errorf("unexpected reason: %s", f.SystemEvents[0].Reason)
	}

	if len(f.SystemPayloads) != 1 {
		t.Fatalf("expected 1 system payload, got %d", len(f.SystemPayloads))
	}
}

func TestFakePublisherPublishSystemError(t *testing.T) {
	f := NewFakePublisher()
	f.PublishSystemError = errors.New("simulated error")

	event := SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	err := f.PublishSystem(event)
	if err == nil {
		t.Error("expected error")
	}

	if len(f.SystemEvents) != 0 {
		t.Errorf("expected no system events recorded on error, got %d", len(f.SystemEvents))
	}
}

func TestFakePublisherResetIncludesSystemEvents(t *testing.T) {
	f := NewFakePublisher()

	// Add heating event
	heatingEvent := logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}
	f.Publish(heatingEvent)

	// Add system event
	systemEvent := SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}
	f.PublishSystem(systemEvent)

	f.PublishSystemError = errors.New("error")

	f.Reset()

	if len(f.Events) != 0 {
		t.Error("events should be cleared")
	}
	if len(f.Payloads) != 0 {
		t.Error("payloads should be cleared")
	}
	if len(f.SystemEvents) != 0 {
		t.Error("system events should be cleared")
	}
	if len(f.SystemPayloads) != 0 {
		t.Error("system payloads should be cleared")
	}
	if f.PublishSystemError != nil {
		t.Error("system error should be cleared")
	}
}

func TestFakePublisherMixedEvents(t *testing.T) {
	f := NewFakePublisher()

	// Publish heating events
	for i := 0; i < 3; i++ {
		f.Publish(logic.Event{
			Timestamp: time.Now(),
			Type:      logic.EventCHOn,
			CHState:   logic.StateOn,
			HWState:   logic.StateOff,
		})
	}

	// Publish system event
	f.PublishSystem(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGINT",
	})

	// Verify counts
	if len(f.Events) != 3 {
		t.Errorf("expected 3 heating events, got %d", len(f.Events))
	}
	if len(f.SystemEvents) != 1 {
		t.Errorf("expected 1 system event, got %d", len(f.SystemEvents))
	}
}

// TestFakePublisherImplementsPublisher verifies interface compliance at compile time.
var _ Publisher = (*FakePublisher)(nil)

func TestFormatPayloadTimezoneConversion(t *testing.T) {
	// Create event with non-UTC timezone
	loc, _ := time.LoadLocation("America/New_York")
	localTime := time.Date(2026, 2, 3, 10, 30, 0, 0, loc) // 10:30 EST = 15:30 UTC

	event := logic.Event{
		Timestamp: localTime,
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	}

	payload, err := FormatPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed Payload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should be converted to UTC
	if parsed.Heating.Timestamp != "2026-02-03T15:30:00Z" {
		t.Errorf("expected UTC timestamp 2026-02-03T15:30:00Z, got %s", parsed.Heating.Timestamp)
	}
}

func TestFormatSystemPayloadTimezoneConversion(t *testing.T) {
	// Create event with non-UTC timezone
	loc, _ := time.LoadLocation("Europe/London")
	localTime := time.Date(2026, 7, 15, 14, 0, 0, 0, loc) // 14:00 BST = 13:00 UTC

	event := SystemEvent{
		Timestamp: localTime,
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should be converted to UTC
	if parsed.System.Timestamp != "2026-07-15T13:00:00Z" {
		t.Errorf("expected UTC timestamp 2026-07-15T13:00:00Z, got %s", parsed.System.Timestamp)
	}
}

func TestFakePublisherPreservesEventOrder(t *testing.T) {
	f := NewFakePublisher()

	events := []logic.EventType{
		logic.EventCHOn,
		logic.EventHWOn,
		logic.EventCHOff,
		logic.EventHWOff,
	}

	for _, eventType := range events {
		f.Publish(logic.Event{
			Timestamp: time.Now(),
			Type:      eventType,
			CHState:   logic.StateOn,
			HWState:   logic.StateOff,
		})
	}

	if len(f.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(f.Events))
	}

	for i, eventType := range events {
		if f.Events[i].Type != eventType {
			t.Errorf("event %d: expected %s, got %s", i, eventType, f.Events[i].Type)
		}
	}
}

func TestFakePublisherPreservesFullEventData(t *testing.T) {
	f := NewFakePublisher()

	timestamp := time.Date(2026, 3, 15, 9, 45, 30, 123456789, time.UTC)
	event := logic.Event{
		Timestamp: timestamp,
		Type:      logic.EventHWOn,
		CHState:   logic.StateOff,
		HWState:   logic.StateOn,
	}

	f.Publish(event)

	recorded := f.Events[0]
	if !recorded.Timestamp.Equal(timestamp) {
		t.Errorf("timestamp not preserved: got %v, want %v", recorded.Timestamp, timestamp)
	}
	if recorded.Type != logic.EventHWOn {
		t.Errorf("event type not preserved: got %s, want HW_ON", recorded.Type)
	}
	if recorded.CHState != logic.StateOff {
		t.Errorf("CH state not preserved: got %s, want OFF", recorded.CHState)
	}
	if recorded.HWState != logic.StateOn {
		t.Errorf("HW state not preserved: got %s, want ON", recorded.HWState)
	}
}

func TestFakePublisherReusableAfterReset(t *testing.T) {
	f := NewFakePublisher()

	// First round of publishes
	f.Publish(logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventCHOn,
		CHState:   logic.StateOn,
		HWState:   logic.StateOff,
	})
	f.PublishSystem(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	})

	if len(f.Events) != 1 || len(f.SystemEvents) != 1 {
		t.Fatal("expected 1 event of each type before reset")
	}

	// Reset
	f.Reset()

	// Second round should work normally
	err := f.Publish(logic.Event{
		Timestamp: time.Now(),
		Type:      logic.EventHWOn,
		CHState:   logic.StateOff,
		HWState:   logic.StateOn,
	})
	if err != nil {
		t.Fatalf("publish after reset failed: %v", err)
	}

	err = f.PublishSystem(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "SIGINT",
	})
	if err != nil {
		t.Fatalf("publish system after reset failed: %v", err)
	}

	if len(f.Events) != 1 {
		t.Errorf("expected 1 event after reset, got %d", len(f.Events))
	}
	if f.Events[0].Type != logic.EventHWOn {
		t.Errorf("expected HW_ON after reset, got %s", f.Events[0].Type)
	}
	if len(f.SystemEvents) != 1 {
		t.Errorf("expected 1 system event after reset, got %d", len(f.SystemEvents))
	}
	if f.SystemEvents[0].Reason != "SIGINT" {
		t.Errorf("expected SIGINT after reset, got %s", f.SystemEvents[0].Reason)
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	original := logic.Event{
		Timestamp: time.Date(2026, 5, 20, 16, 45, 30, 0, time.UTC),
		Type:      logic.EventCHOff,
		CHState:   logic.StateOff,
		HWState:   logic.StateOn,
	}

	payload, err := FormatPayload(original)
	if err != nil {
		t.Fatalf("format error: %v", err)
	}

	var parsed Payload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Verify round-trip preserves data
	if parsed.Heating.Event != string(original.Type) {
		t.Errorf("event type mismatch: got %s, want %s", parsed.Heating.Event, original.Type)
	}
	if parsed.Heating.CH.State != string(original.CHState) {
		t.Errorf("CH state mismatch: got %s, want %s", parsed.Heating.CH.State, original.CHState)
	}
	if parsed.Heating.HW.State != string(original.HWState) {
		t.Errorf("HW state mismatch: got %s, want %s", parsed.Heating.HW.State, original.HWState)
	}

	// Parse the timestamp back
	parsedTime, err := time.Parse(time.RFC3339, parsed.Heating.Timestamp)
	if err != nil {
		t.Fatalf("timestamp parse error: %v", err)
	}
	if !parsedTime.Equal(original.Timestamp) {
		t.Errorf("timestamp mismatch: got %v, want %v", parsedTime, original.Timestamp)
	}
}

func TestSystemPayloadRoundTrip(t *testing.T) {
	original := SystemEvent{
		Timestamp: time.Date(2026, 8, 10, 23, 59, 59, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
	}

	payload, err := FormatSystemPayload(original)
	if err != nil {
		t.Fatalf("format error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if parsed.System.Event != original.Event {
		t.Errorf("event mismatch: got %s, want %s", parsed.System.Event, original.Event)
	}
	if parsed.System.Reason != original.Reason {
		t.Errorf("reason mismatch: got %s, want %s", parsed.System.Reason, original.Reason)
	}

	parsedTime, err := time.Parse(time.RFC3339, parsed.System.Timestamp)
	if err != nil {
		t.Fatalf("timestamp parse error: %v", err)
	}
	if !parsedTime.Equal(original.Timestamp) {
		t.Errorf("timestamp mismatch: got %v, want %v", parsedTime, original.Timestamp)
	}
}

func TestFormatSystemPayloadHeartbeat(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: HeartbeatCounts{
				CHOn:  5,
				CHOff: 4,
				HWOn:  2,
				HWOff: 2,
			},
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Timestamp != "2026-02-04T12:15:00Z" {
		t.Errorf("unexpected timestamp: %s", parsed.System.Timestamp)
	}
	if parsed.System.Event != "HEARTBEAT" {
		t.Errorf("unexpected event: %s", parsed.System.Event)
	}
	if parsed.System.Heartbeat == nil {
		t.Fatal("expected heartbeat to be present")
	}
	if parsed.System.Heartbeat.UptimeSeconds != 900 {
		t.Errorf("unexpected uptime_seconds: %d", parsed.System.Heartbeat.UptimeSeconds)
	}
	if parsed.System.Heartbeat.EventCounts.CHOn != 5 {
		t.Errorf("unexpected ch_on: %d", parsed.System.Heartbeat.EventCounts.CHOn)
	}
	if parsed.System.Heartbeat.EventCounts.CHOff != 4 {
		t.Errorf("unexpected ch_off: %d", parsed.System.Heartbeat.EventCounts.CHOff)
	}
	if parsed.System.Heartbeat.EventCounts.HWOn != 2 {
		t.Errorf("unexpected hw_on: %d", parsed.System.Heartbeat.EventCounts.HWOn)
	}
	if parsed.System.Heartbeat.EventCounts.HWOff != 2 {
		t.Errorf("unexpected hw_off: %d", parsed.System.Heartbeat.EventCounts.HWOff)
	}
}

func TestFormatSystemPayloadHeartbeatExactJSON(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: HeartbeatCounts{
				CHOn:  5,
				CHOff: 4,
				HWOn:  2,
				HWOff: 2,
			},
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-04T12:15:00Z","event":"HEARTBEAT","heartbeat":{"uptime_seconds":900,"event_counts":{"ch_on":5,"ch_off":4,"hw_on":2,"hw_off":2}}}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadHeartbeatOmitsOtherFields(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts:   HeartbeatCounts{},
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reason and Config should be omitted
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	system := parsed["system"].(map[string]interface{})
	if _, exists := system["reason"]; exists {
		t.Error("reason field should be omitted for heartbeat events")
	}
	if _, exists := system["config"]; exists {
		t.Error("config field should be omitted for heartbeat events")
	}
}

func TestFormatSystemPayloadStartupWithNetwork(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 19, 5, 51, 0, time.UTC),
		Event:     "STARTUP",
		Config: &SystemConfig{
			PollMs:      100,
			DebounceMs:  250,
			HeartbeatMs: 900000,
			Broker:      "tcp://192.168.1.200:1883",
		},
		Network: &NetworkInfo{
			Type:       "wifi",
			IP:         "192.168.1.100",
			Status:     "connected",
			Gateway:    "192.168.1.1",
			WifiStatus: "connected",
			SSID:       "MyNetwork",
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-03T19:05:51Z","event":"STARTUP","config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":900000,"broker":"tcp://192.168.1.200:1883"},"network":{"type":"wifi","ip":"192.168.1.100","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"MyNetwork"}}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadHeartbeatWithNetwork(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 4, 12, 15, 0, 0, time.UTC),
		Event:     "HEARTBEAT",
		Heartbeat: &HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: HeartbeatCounts{
				CHOn:  5,
				CHOff: 4,
				HWOn:  2,
				HWOff: 2,
			},
		},
		Network: &NetworkInfo{
			Type:       "wifi",
			IP:         "192.168.1.100",
			Status:     "connected",
			Gateway:    "192.168.1.1",
			WifiStatus: "connected",
			SSID:       "MyNetwork",
		},
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-04T12:15:00Z","event":"HEARTBEAT","heartbeat":{"uptime_seconds":900,"event_counts":{"ch_on":5,"ch_off":4,"hw_on":2,"hw_off":2}},"network":{"type":"wifi","ip":"192.168.1.100","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"MyNetwork"}}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadNetworkOmittedWhenNil(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 3, 10, 30, 45, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "SIGTERM",
		Network:   nil,
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	system := parsed["system"].(map[string]interface{})
	if _, exists := system["network"]; exists {
		t.Error("network field should be omitted when nil")
	}
}

func TestWillPayloadFormat(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 10, 8, 30, 0, 0, time.UTC),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed SystemPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.System.Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN event, got %s", parsed.System.Event)
	}
	if parsed.System.Reason != "MQTT_DISCONNECT" {
		t.Errorf("expected MQTT_DISCONNECT reason, got %s", parsed.System.Reason)
	}
	if parsed.System.Timestamp != "2026-02-10T08:30:00Z" {
		t.Errorf("unexpected timestamp: %s", parsed.System.Timestamp)
	}

	expected := `{"system":{"timestamp":"2026-02-10T08:30:00Z","event":"SHUTDOWN","reason":"MQTT_DISCONNECT"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}
}

func TestFormatSystemPayloadReconnected(t *testing.T) {
	event := SystemEvent{
		Timestamp: time.Date(2026, 2, 10, 14, 30, 0, 0, time.UTC),
		Event:     "RECONNECTED",
	}

	payload, err := FormatSystemPayload(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"system":{"timestamp":"2026-02-10T14:30:00Z","event":"RECONNECTED"}}`
	if string(payload) != expected {
		t.Errorf("unexpected payload:\ngot:  %s\nwant: %s", string(payload), expected)
	}

	// Verify no reason, config, heartbeat, or network
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	system := parsed["system"].(map[string]interface{})
	if _, exists := system["reason"]; exists {
		t.Error("RECONNECTED should not have reason field")
	}
	if _, exists := system["config"]; exists {
		t.Error("RECONNECTED should not have config field")
	}
	if _, exists := system["heartbeat"]; exists {
		t.Error("RECONNECTED should not have heartbeat field")
	}
	if _, exists := system["network"]; exists {
		t.Error("RECONNECTED should not have network field")
	}
}

func TestFakePublisherRecordsRetainedFlag(t *testing.T) {
	f := NewFakePublisher()

	retained := SystemEvent{
		Timestamp: time.Now(),
		Event:     "STARTUP",
		Retained:  true,
	}
	notRetained := SystemEvent{
		Timestamp: time.Now(),
		Event:     "HEARTBEAT",
		Retained:  false,
	}

	f.PublishSystem(retained)
	f.PublishSystem(notRetained)

	if len(f.SystemEvents) != 2 {
		t.Fatalf("expected 2 system events, got %d", len(f.SystemEvents))
	}
	if !f.SystemEvents[0].Retained {
		t.Error("first event should have Retained=true")
	}
	if f.SystemEvents[1].Retained {
		t.Error("second event should have Retained=false")
	}
}

func TestFakePublisherPublishSystemHeartbeat(t *testing.T) {
	f := NewFakePublisher()

	event := SystemEvent{
		Timestamp: time.Now(),
		Event:     "HEARTBEAT",
		Heartbeat: &HeartbeatInfo{
			UptimeSeconds: 900,
			EventCounts: HeartbeatCounts{
				CHOn:  1,
				CHOff: 0,
				HWOn:  0,
				HWOff: 0,
			},
		},
	}

	err := f.PublishSystem(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(f.SystemEvents))
	}

	if f.SystemEvents[0].Event != "HEARTBEAT" {
		t.Errorf("unexpected event: %s", f.SystemEvents[0].Event)
	}
	if f.SystemEvents[0].Heartbeat == nil {
		t.Fatal("expected heartbeat to be present")
	}
	if f.SystemEvents[0].Heartbeat.UptimeSeconds != 900 {
		t.Errorf("unexpected uptime: %d", f.SystemEvents[0].Heartbeat.UptimeSeconds)
	}
}
