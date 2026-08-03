package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	prof := profile.SensorProfile{
		Channels: []profile.Channel{
			{Key: "ch", Label: "Central Heating", OnEvent: "CH_ON", OffEvent: "CH_OFF"},
			{Key: "hw", Label: "Hot Water", OnEvent: "HW_ON", OffEvent: "HW_OFF"},
		},
		PayloadRoot: "boiler",
		EventTopic:  "energy/boiler/sensor/events",
		WebTitle:    "Boiler Sensor",
	}
	tracker := status.NewTrackerForProfile(prof, time.Now(), status.Config{Broker: "tcp://localhost:1883"})
	tracker.UpdateChannels(nil, true, map[string]int{"CH_ON": 1})
	return NewWithView(":0", tracker, ViewForProfile(prof))
}

func TestHandleIndexHTML(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"Boiler Sensor", "Central Heating", "Hot Water", "state-ch", "state-hw"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestHandleJSON(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	srv.handleJSON(rec, httptest.NewRequest(http.MethodGet, "/index.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"status"`) {
		t.Errorf("JSON missing status root: %s", body)
	}
}

func TestHandleIndexNotFound(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}
