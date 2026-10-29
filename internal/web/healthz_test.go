package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

func healthTracker() *status.Tracker {
	prof := profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "ch", OnEvent: "CH_ON", OffEvent: "CH_OFF"},
			{Key: "hw", OnEvent: "HW_ON", OffEvent: "HW_OFF"},
		},
	}
	return status.NewTrackerForProfile(prof, time.Now().Add(-90*time.Second), status.Config{})
}

func getHealthz(t *testing.T, tracker *status.Tracker) map[string]any {
	t.Helper()
	srv := NewWithView(":0", tracker, View{})
	srv.Version = "abc1234"
	rec := httptest.NewRecorder()
	// Through the mux, so the route itself is tested, not just the handler.
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control: got %q", cc)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rec.Body)
	}
	return body
}

func TestHealthzOK(t *testing.T) {
	tr := healthTracker()
	tr.UpdateChannels(map[string]logic.State{"ch": logic.StateOn, "hw": logic.StateOff}, true, nil)
	tr.SetMQTTConnected(true)

	b := getHealthz(t, tr)
	if b["status"] != "ok" {
		t.Errorf("status: got %v, want ok", b["status"])
	}
	if _, ok := b["detail"]; ok {
		t.Errorf("detail should be absent when ok, got %v", b["detail"])
	}
	if b["version"] != "abc1234" {
		t.Errorf("version: got %v", b["version"])
	}
	if b["ready"] != true || b["mqtt_connected"] != true {
		t.Errorf("ready/mqtt_connected: got %v/%v", b["ready"], b["mqtt_connected"])
	}
	ch := b["channels"].(map[string]any)
	if ch["ch"] != "ON" || ch["hw"] != "OFF" {
		t.Errorf("channels: got %v", ch)
	}
	if up := b["uptime_s"].(float64); up < 89 || up > 120 {
		t.Errorf("uptime_s: got %v, want ~90", up)
	}
	for _, k := range []string{"start_time", "goroutines", "heap_mb", "sys_mb"} {
		if _, ok := b[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
}

func TestHealthzStartingBeforeBaseline(t *testing.T) {
	tr := healthTracker()
	tr.SetMQTTConnected(true)

	b := getHealthz(t, tr)
	if b["status"] != "starting" {
		t.Errorf("status: got %v, want starting", b["status"])
	}
	ch := b["channels"].(map[string]any)
	if ch["ch"] != "UNKNOWN" || ch["hw"] != "UNKNOWN" {
		t.Errorf("channels before baseline: got %v, want UNKNOWN", ch)
	}
}

func TestHealthzDegradedWithoutMQTT(t *testing.T) {
	tr := healthTracker()
	tr.UpdateChannels(map[string]logic.State{"ch": logic.StateOff, "hw": logic.StateOff}, true, nil)

	b := getHealthz(t, tr)
	if b["status"] != "degraded" {
		t.Errorf("status: got %v, want degraded", b["status"])
	}
	if b["detail"] == nil {
		t.Error("degraded should say why")
	}
}
