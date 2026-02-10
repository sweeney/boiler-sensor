package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
	"github.com/sweeney/boiler-sensor/internal/status"
)

func newTestServer(t *testing.T) (*httptest.Server, *status.Tracker) {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := status.Config{
		PollMs:      100,
		DebounceMs:  250,
		HeartbeatMs: 900000,
		Broker:      "tcp://192.168.1.200:1883",
		HTTPAddr:    ":80",
	}
	tr := status.NewTracker(start, cfg)
	srv := New(":0", tr)
	ts := httptest.NewServer(srv.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts, tr
}

func TestJSONEndpoint(t *testing.T) {
	ts, tr := newTestServer(t)
	tr.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{CHOn: 5, CHOff: 2})
	tr.SetMQTTConnected(true)

	resp, err := http.Get(ts.URL + "/index.json")
	if err != nil {
		t.Fatalf("GET /index.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	var sj StatusJSON
	if err := json.NewDecoder(resp.Body).Decode(&sj); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	if sj.Status.CH != "ON" {
		t.Errorf("CH: got %q, want ON", sj.Status.CH)
	}
	if sj.Status.HW != "OFF" {
		t.Errorf("HW: got %q, want OFF", sj.Status.HW)
	}
	if !sj.Status.Ready {
		t.Error("expected Ready=true")
	}
	if !sj.Status.MQTT.Connected {
		t.Error("expected MQTT.Connected=true")
	}
	if sj.Status.MQTT.Broker != "tcp://192.168.1.200:1883" {
		t.Errorf("MQTT.Broker: got %q, want tcp://192.168.1.200:1883", sj.Status.MQTT.Broker)
	}
	if sj.Status.Counts.CHOn != 5 {
		t.Errorf("Counts.CHOn: got %d, want 5", sj.Status.Counts.CHOn)
	}
	if sj.Status.Counts.CHOff != 2 {
		t.Errorf("Counts.CHOff: got %d, want 2", sj.Status.Counts.CHOff)
	}
	if sj.Status.Config.PollMs != 100 {
		t.Errorf("Config.PollMs: got %d, want 100", sj.Status.Config.PollMs)
	}
	if sj.Status.Config.Broker != "tcp://192.168.1.200:1883" {
		t.Errorf("Config.Broker: got %q", sj.Status.Config.Broker)
	}
}

func TestJSONUnknownStateBeforeBaseline(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/index.json")
	if err != nil {
		t.Fatalf("GET /index.json: %v", err)
	}
	defer resp.Body.Close()

	var sj StatusJSON
	json.NewDecoder(resp.Body).Decode(&sj)

	if sj.Status.CH != "UNKNOWN" {
		t.Errorf("CH before baseline: got %q, want UNKNOWN", sj.Status.CH)
	}
	if sj.Status.HW != "UNKNOWN" {
		t.Errorf("HW before baseline: got %q, want UNKNOWN", sj.Status.HW)
	}
}

func TestJSONNetworkInfo(t *testing.T) {
	ts, tr := newTestServer(t)
	tr.SetNetwork(&status.NetworkInfo{
		Type:   "wifi",
		IP:     "192.168.1.42",
		Status: "connected",
		SSID:   "MyNet",
	})

	resp, err := http.Get(ts.URL + "/index.json")
	if err != nil {
		t.Fatalf("GET /index.json: %v", err)
	}
	defer resp.Body.Close()

	var sj StatusJSON
	json.NewDecoder(resp.Body).Decode(&sj)

	if sj.Status.Network == nil {
		t.Fatal("expected Network in JSON")
	}
	if sj.Status.Network.IP != "192.168.1.42" {
		t.Errorf("Network.IP: got %q, want 192.168.1.42", sj.Status.Network.IP)
	}
}

func TestHTMLEndpointRoot(t *testing.T) {
	ts, tr := newTestServer(t)
	tr.Update(logic.StateOn, logic.StateOff, true, logic.EventCounts{})

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
}

func TestHTMLEndpointIndexHTML(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/index.html")
	if err != nil {
		t.Fatalf("GET /index.html: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestNotFoundForUnknownPath(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/nonexistent")
	if err != nil {
		t.Fatalf("GET /nonexistent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestStateChangesReflectedInResponse(t *testing.T) {
	ts, tr := newTestServer(t)

	// Initially not baselined
	resp1, _ := http.Get(ts.URL + "/index.json")
	var sj1 StatusJSON
	json.NewDecoder(resp1.Body).Decode(&sj1)
	resp1.Body.Close()
	if sj1.Status.Ready {
		t.Error("expected Ready=false initially")
	}

	// Update state
	tr.Update(logic.StateOff, logic.StateOn, true, logic.EventCounts{HWOn: 1})
	tr.SetMQTTConnected(true)

	// Should reflect new state
	resp2, _ := http.Get(ts.URL + "/index.json")
	var sj2 StatusJSON
	json.NewDecoder(resp2.Body).Decode(&sj2)
	resp2.Body.Close()

	if !sj2.Status.Ready {
		t.Error("expected Ready=true after update")
	}
	if sj2.Status.HW != "ON" {
		t.Errorf("HW: got %q, want ON", sj2.Status.HW)
	}
	if !sj2.Status.MQTT.Connected {
		t.Error("expected MQTT connected after update")
	}
}
