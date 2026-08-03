package main

import (
	"testing"

	"github.com/sweeney/common/iot/profile"
)

// TestBoilerProfile pins the boiler-specific profile vocabulary: the engine is
// generic, so this is the contract that the boiler binary supplies the right
// channels, events, topics, root key, and title. (Engine behaviour — debounce,
// baseline, heartbeat, shutdown — is tested in github.com/sweeney/common/iot.)
func TestBoilerProfile(t *testing.T) {
	prof := boilerProfile(26, 16)

	if prof.PayloadRoot != "boiler" {
		t.Errorf("PayloadRoot: got %q, want boiler", prof.PayloadRoot)
	}
	if prof.EventTopic != "energy/boiler/sensor/events" {
		t.Errorf("EventTopic: got %q", prof.EventTopic)
	}
	if prof.SystemTopic != "energy/boiler/sensor/system" {
		t.Errorf("SystemTopic: got %q", prof.SystemTopic)
	}
	if prof.WebTitle != "Boiler Sensor" {
		t.Errorf("WebTitle: got %q", prof.WebTitle)
	}
	if len(prof.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(prof.Channels))
	}

	wantCh := []profile.Channel{
		{Key: "ch", Label: "Central Heating", Pin: 26, ActiveLow: true, OnEvent: "CH_ON", OffEvent: "CH_OFF"},
		{Key: "hw", Label: "Hot Water", Pin: 16, ActiveLow: true, OnEvent: "HW_ON", OffEvent: "HW_OFF"},
	}
	for i, want := range wantCh {
		if prof.Channels[i] != want {
			t.Errorf("channel %d: got %+v, want %+v", i, prof.Channels[i], want)
		}
	}
}

func TestResolveWSBroker(t *testing.T) {
	tests := []struct {
		name   string
		ws     string
		broker string
		want   string
	}{
		{"derives from broker", "=broker", "tcp://192.168.1.200:1883", "ws://192.168.1.200:9001"},
		{"explicit URL", "ws://10.0.0.1:8080", "tcp://192.168.1.200:1883", "ws://10.0.0.1:8080"},
		{"off disables", "off", "tcp://192.168.1.200:1883", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveWSBroker(tt.ws, tt.broker)
			if got != tt.want {
				t.Errorf("resolveWSBroker(%q, %q) = %q, want %q", tt.ws, tt.broker, got, tt.want)
			}
		})
	}
}
