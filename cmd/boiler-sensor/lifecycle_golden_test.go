package main

import (
	"bytes"
	"log"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sweeney/common/iot"
	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/status"
)

// TestLifecycleGolden pins the EXACT ordered MQTT output of a whole daemon
// lifecycle, driven through the real runtime (iot.Run) with the real
// boilerProfile(): STARTUP, baseline + ready HEARTBEAT, single transitions
// (CH_ON, HW_ON, HW_OFF, HW_ON), a simultaneous CH_OFF+HW_OFF in one tick, a
// simultaneous CH_ON+HW_ON in one tick, three scheduled HEARTBEATs carrying the
// network block and a non-empty ws_broker, then SIGTERM -> SHUTDOWN. It also
// pins the scheduled-heartbeat journal line, which operators grep.
//
// The expected literals were CAPTURED FROM THE PRE-REFACTOR PRODUCTION BUILD
// (boiler-sensor main @ cdb29ce, old runLoop) fed the identical inputs. They
// are the contract the broker has always received. DO NOT edit them to make a
// failing test pass — a diff here is a wire regression unless the change is
// deliberate, in which case update the goldens deliberately.
//
// Volatile fields: the tracker stamps STARTUP and SHUTDOWN with time.Now()
// inside Snapshot(), which no injection point controls, so for those two
// payloads only "timestamp" and "uptime_seconds" are normalised by
// normaliseWallClock (the timestamp must still be RFC3339 UTC). Everything
// else — including every HEARTBEAT timestamp/uptime, which the runtime takes
// from the injected clock — is compared byte for byte. (The old build took the
// heartbeat time from the wall clock at the same point in the same tick; with a
// real clock that is the tick time, which is what is pinned here.)
//
// Topics: the FakePublisher does not record topics, so the recorder below
// stamps each publish with the topic RealPublisher uses for that kind (the
// profile's EventTopic for events, SystemTopic for system messages).
func TestLifecycleGolden(t *testing.T) {
	prof := boilerProfile(defaultPinCH, defaultPinHW)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const poll = 100 * time.Millisecond

	// Logical (post-inversion) samples, [ch, hw], one per poll tick. Tick k
	// happens at start + k*100ms (the runtime's first clock read, start, is
	// the detector's start time).
	var samples [][]bool
	add := func(ch, hw bool, n int) {
		for i := 0; i < n; i++ {
			samples = append(samples, []bool{ch, hw})
		}
	}
	add(false, false, 4) // t=100..400ms   - baseline at 400ms -> ready HEARTBEAT
	add(true, false, 4)  // t=500..800ms   - CH_ON at 800ms; scheduled HEARTBEAT at 1000ms
	add(true, true, 4)   // t=900..1200ms  - HW_ON at 1200ms
	add(true, false, 4)  // t=1300..1600ms - HW_OFF at 1600ms
	add(true, true, 4)   // t=1700..2000ms - HW_ON at 2000ms, then scheduled HEARTBEAT
	add(false, false, 4) // t=2100..2400ms - CH_OFF + HW_OFF in one tick
	add(true, true, 6)   // t=2500..3000ms - CH_ON + HW_ON in one tick; HEARTBEAT at 3000ms

	var calls int64
	clock := func() time.Time { // only ever called from the Run goroutine
		c := calls
		calls++
		return start.Add(time.Duration(c) * poll)
	}
	network := &status.NetworkInfo{
		Type: "wifi", IP: "192.168.1.50", Status: "connected",
		Gateway: "192.168.1.1", WifiStatus: "connected", SSID: "Home",
	}
	tracker := status.NewTrackerForProfile(prof, start, status.Config{
		PollMs:      100,
		DebounceMs:  250,
		HeartbeatMs: 1000,
		Broker:      "tcp://192.168.1.200:1883",
		HTTPPort:    ":80",
		WSBroker:    "ws://192.168.1.200:9001",
	})
	fake := mqtt.NewFakePublisherForProfile(prof)
	fake.Connected = true
	rec := &recordingPublisher{FakePublisher: fake, eventTopic: prof.EventTopic, systemTopic: prof.SystemTopic}
	var logBuf bytes.Buffer

	tick := make(chan time.Time) // unbuffered: each send hands over exactly one tick
	sig := make(chan os.Signal, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- iot.Run(iot.RunConfig{
			Profile:     prof,
			Reader:      gpio.NewFakeChannelReader(samples),
			Publisher:   rec,
			Tracker:     tracker,
			Debounce:    250 * time.Millisecond,
			Heartbeat:   time.Second,
			Poll:        poll,
			Now:         clock,
			Tick:        tick,
			Signals:     sig,
			NetworkInfo: func() *status.NetworkInfo { return network },
			Logger:      log.New(&logBuf, "", 0),
		})
	}()
	for range samples {
		tick <- start
	}
	// The last tick has been received, so the signal is handled after it.
	sig <- syscall.SIGTERM
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}

	const (
		evT  = "energy/boiler/sensor/events"
		sysT = "energy/boiler/sensor/system"
	)
	// Frozen boiler wire contract (captured from main @ cdb29ce) — DO NOT CHANGE.
	want := []published{
		{"system", sysT, false, `{"status":{"event":"STARTUP","ch":"UNKNOWN","hw":"UNKNOWN","ready":false,"uptime_seconds":<WALLCLOCK>,"start_time":"2026-01-01T12:00:00Z","timestamp":"<WALLCLOCK>","mqtt":{"connected":false,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":0,"ch_off":0,"hw_on":0,"hw_off":0},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
		{"system", sysT, true, `{"status":{"event":"HEARTBEAT","ch":"OFF","hw":"OFF","ready":true,"uptime_seconds":0,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:00:00Z","mqtt":{"connected":true,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":0,"ch_off":0,"hw_on":0,"hw_off":0},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:00Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`},
		{"system", sysT, true, `{"status":{"event":"HEARTBEAT","ch":"ON","hw":"OFF","ready":true,"uptime_seconds":1,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:00:01Z","mqtt":{"connected":true,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":1,"ch_off":0,"hw_on":0,"hw_off":0},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:01Z","event":"HW_OFF","ch":{"state":"ON"},"hw":{"state":"OFF"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:02Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`},
		{"system", sysT, true, `{"status":{"event":"HEARTBEAT","ch":"ON","hw":"ON","ready":true,"uptime_seconds":2,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:00:02Z","mqtt":{"connected":true,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":1,"ch_off":0,"hw_on":2,"hw_off":1},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:02Z","event":"CH_OFF","ch":{"state":"OFF"},"hw":{"state":"OFF"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:02Z","event":"HW_OFF","ch":{"state":"OFF"},"hw":{"state":"OFF"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:02Z","event":"CH_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`},
		{"event", evT, false, `{"boiler":{"timestamp":"2026-01-01T12:00:02Z","event":"HW_ON","ch":{"state":"ON"},"hw":{"state":"ON"}}}`},
		{"system", sysT, true, `{"status":{"event":"HEARTBEAT","ch":"ON","hw":"ON","ready":true,"uptime_seconds":3,"start_time":"2026-01-01T12:00:00Z","timestamp":"2026-01-01T12:00:03Z","mqtt":{"connected":true,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":2,"ch_off":1,"hw_on":3,"hw_off":2},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
		{"system", sysT, true, `{"status":{"event":"SHUTDOWN","reason":"SIGTERM","ch":"ON","hw":"ON","ready":true,"uptime_seconds":<WALLCLOCK>,"start_time":"2026-01-01T12:00:00Z","timestamp":"<WALLCLOCK>","mqtt":{"connected":true,"broker":"tcp://192.168.1.200:1883"},"event_counts":{"ch_on":2,"ch_off":1,"hw_on":3,"hw_off":2},"network":{"type":"wifi","ip":"192.168.1.50","status":"connected","gateway":"192.168.1.1","wifi_status":"connected","ssid":"Home"},"config":{"poll_ms":100,"debounce_ms":250,"heartbeat_ms":1000,"broker":"tcp://192.168.1.200:1883","http_port":":80","ws_broker":"ws://192.168.1.200:9001"}}}`},
	}

	got := rec.log
	if len(got) != len(want) {
		for i, g := range got {
			t.Logf("got[%d]: %s %s retained=%v %s", i, g.kind, g.topic, g.retained, g.payload)
		}
		t.Fatalf("expected %d publishes, got %d", len(want), len(got))
	}
	for i, w := range want {
		g := got[i]
		payload := g.payload
		if i == 0 || i == len(want)-1 { // STARTUP and SHUTDOWN only
			payload = normaliseWallClock(t, payload)
		}
		if g.kind != w.kind || g.topic != w.topic || g.retained != w.retained {
			t.Errorf("publish %d: got %s %s retained=%v, want %s %s retained=%v",
				i, g.kind, g.topic, g.retained, w.kind, w.topic, w.retained)
		}
		if payload != w.payload {
			t.Errorf("publish %d payload:\n got:  %s\n want: %s", i, payload, w.payload)
		}
	}

	// Scheduled-heartbeat journal lines (captured from main @ cdb29ce) — DO NOT CHANGE.
	wantHeartbeatLogs := []string{
		"heartbeat: uptime=1s mqtt=connected net=connected/192.168.1.50 (wifi=connected ssid=Home) ch_on=1 ch_off=0 hw_on=0 hw_off=0",
		"heartbeat: uptime=2s mqtt=connected net=connected/192.168.1.50 (wifi=connected ssid=Home) ch_on=1 ch_off=0 hw_on=2 hw_off=1",
		"heartbeat: uptime=3s mqtt=connected net=connected/192.168.1.50 (wifi=connected ssid=Home) ch_on=2 ch_off=1 hw_on=3 hw_off=2",
	}
	var gotHeartbeatLogs []string
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.HasPrefix(line, "heartbeat: ") {
			gotHeartbeatLogs = append(gotHeartbeatLogs, line)
		}
	}
	if len(gotHeartbeatLogs) != len(wantHeartbeatLogs) {
		t.Fatalf("expected %d heartbeat log lines, got %d:\n%s", len(wantHeartbeatLogs), len(gotHeartbeatLogs), logBuf.String())
	}
	for i, w := range wantHeartbeatLogs {
		if gotHeartbeatLogs[i] != w {
			t.Errorf("heartbeat log %d:\n got:  %s\n want: %s", i, gotHeartbeatLogs[i], w)
		}
	}
}

// published is one MQTT publish as the broker would see it.
type published struct {
	kind     string // "event" or "system"
	topic    string
	retained bool
	payload  string
}

// recordingPublisher wraps the iot FakePublisher to keep events and system
// messages in ONE ordered log (the fake keeps them in separate slices), with
// the topic RealPublisher would use for each kind. Events are always
// published non-retained (QoS 0) by RealPublisher.
type recordingPublisher struct {
	*mqtt.FakePublisher
	eventTopic, systemTopic string
	log                     []published
}

func (r *recordingPublisher) PublishEvent(e logic.GenericEvent) error {
	if err := r.FakePublisher.PublishEvent(e); err != nil {
		return err
	}
	p := r.FakePublisher.Payloads[len(r.FakePublisher.Payloads)-1]
	r.log = append(r.log, published{"event", r.eventTopic, false, string(p)})
	return nil
}

func (r *recordingPublisher) PublishSystem(e mqtt.SystemEvent) error {
	if err := r.FakePublisher.PublishSystem(e); err != nil {
		return err
	}
	p := r.FakePublisher.SystemPayloads[len(r.FakePublisher.SystemPayloads)-1]
	r.log = append(r.log, published{"system", r.systemTopic, e.Retained, string(p)})
	return nil
}

var (
	wallClockTimestamp = regexp.MustCompile(`"timestamp":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ"`)
	wallClockUptime    = regexp.MustCompile(`"uptime_seconds":\d+`)
)

// normaliseWallClock replaces the wall-clock "timestamp" (which must still be
// RFC3339 UTC with whole seconds) and "uptime_seconds" of a STARTUP/SHUTDOWN
// status payload with <WALLCLOCK>. Each must occur exactly once.
func normaliseWallClock(t *testing.T, payload string) string {
	t.Helper()
	if n := len(wallClockTimestamp.FindAllString(payload, -1)); n != 1 {
		t.Errorf("expected exactly one RFC3339 UTC timestamp, found %d in %s", n, payload)
	}
	if n := len(wallClockUptime.FindAllString(payload, -1)); n != 1 {
		t.Errorf("expected exactly one uptime_seconds, found %d in %s", n, payload)
	}
	payload = wallClockTimestamp.ReplaceAllString(payload, `"timestamp":"<WALLCLOCK>"`)
	return wallClockUptime.ReplaceAllString(payload, `"uptime_seconds":<WALLCLOCK>`)
}
