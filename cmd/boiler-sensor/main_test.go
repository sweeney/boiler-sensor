package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/sweeney/boiler-sensor/internal/gpio"
	"github.com/sweeney/boiler-sensor/internal/mqtt"
)

// TestEnvVarNames verifies the env var constants match what pi-helper writes
// to /run/pi-helper.env. If pi-helper changes its var names, this test fails
// and we update the constants — not the other way around.
func TestEnvVarNames(t *testing.T) {
	// These are the canonical names from pi-helper.
	want := map[string]string{
		"NETWORK_TYPE":        envNetworkType,
		"NETWORK_IP":          envNetworkIP,
		"NETWORK_STATUS":      envNetworkStatus,
		"NETWORK_GATEWAY":     envNetworkGateway,
		"NETWORK_WIFI_STATUS": envNetworkWifiStatus,
		"NETWORK_WIFI_SSID":   envNetworkWifiSSID,
	}
	for canonical, got := range want {
		if got != canonical {
			t.Errorf("env var constant: got %q, want %q", got, canonical)
		}
	}
}

func TestReadNetworkInfoAllSet(t *testing.T) {
	t.Setenv(envNetworkType, "wifi")
	t.Setenv(envNetworkIP, "192.168.1.100")
	t.Setenv(envNetworkStatus, "connected")
	t.Setenv(envNetworkGateway, "192.168.1.1")
	t.Setenv(envNetworkWifiStatus, "connected")
	t.Setenv(envNetworkWifiSSID, "MyNetwork")

	info := readNetworkInfo()
	if info == nil {
		t.Fatal("expected non-nil NetworkInfo")
	}

	want := &mqtt.NetworkInfo{
		Type:       "wifi",
		IP:         "192.168.1.100",
		Status:     "connected",
		Gateway:    "192.168.1.1",
		WifiStatus: "connected",
		SSID:       "MyNetwork",
	}

	if info.Type != want.Type {
		t.Errorf("Type: got %q, want %q", info.Type, want.Type)
	}
	if info.IP != want.IP {
		t.Errorf("IP: got %q, want %q", info.IP, want.IP)
	}
	if info.Status != want.Status {
		t.Errorf("Status: got %q, want %q", info.Status, want.Status)
	}
	if info.Gateway != want.Gateway {
		t.Errorf("Gateway: got %q, want %q", info.Gateway, want.Gateway)
	}
	if info.WifiStatus != want.WifiStatus {
		t.Errorf("WifiStatus: got %q, want %q", info.WifiStatus, want.WifiStatus)
	}
	if info.SSID != want.SSID {
		t.Errorf("SSID: got %q, want %q", info.SSID, want.SSID)
	}
}

func TestReadNetworkInfoNoneSet(t *testing.T) {
	info := readNetworkInfo()
	if info != nil {
		t.Errorf("expected nil when NETWORK_STATUS is unset, got %+v", info)
	}
}

func TestReadNetworkInfoPartial(t *testing.T) {
	t.Setenv(envNetworkStatus, "connected")

	info := readNetworkInfo()
	if info == nil {
		t.Fatal("expected non-nil NetworkInfo when NETWORK_STATUS is set")
	}

	if info.Status != "connected" {
		t.Errorf("Status: got %q, want %q", info.Status, "connected")
	}
	if info.Type != "" {
		t.Errorf("Type: got %q, want empty", info.Type)
	}
	if info.IP != "" {
		t.Errorf("IP: got %q, want empty", info.IP)
	}
	if info.Gateway != "" {
		t.Errorf("Gateway: got %q, want empty", info.Gateway)
	}
	if info.WifiStatus != "" {
		t.Errorf("WifiStatus: got %q, want empty", info.WifiStatus)
	}
	if info.SSID != "" {
		t.Errorf("SSID: got %q, want empty", info.SSID)
	}
}

// --- runLoop tests ---

// fakeClock returns a function that yields start, start+step, start+2*step, ...
// on successive calls. Not safe for concurrent use (only called from runLoop's goroutine).
func fakeClock(start time.Time, step time.Duration) func() time.Time {
	n := 0
	return func() time.Time {
		t := start.Add(time.Duration(n) * step)
		n++
		return t
	}
}

// repeat returns n copies of sample.
func repeat(sample gpio.Sample, n int) []gpio.Sample {
	out := make([]gpio.Sample, n)
	for i := range out {
		out[i] = sample
	}
	return out
}

// runRunLoop drives runLoop with the given samples and signal, returning
// the error and the fake publisher for assertions.
func runRunLoop(t *testing.T, reader gpio.Reader, pub *mqtt.FakePublisher, debounce, heartbeat time.Duration, clock func() time.Time, nTicks int, signal os.Signal) error {
	t.Helper()
	tick := make(chan time.Time)
	sig := make(chan os.Signal, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runLoop(reader, pub, debounce, heartbeat, clock, tick, sig)
	}()

	for i := 0; i < nTicks; i++ {
		tick <- time.Time{}
	}
	sig <- signal

	return <-errCh
}

func TestRunLoopNoEventsAtBaseline(t *testing.T) {
	// 4 ticks of stable (both off) → should establish baseline, emit no heating events
	samples := repeat(gpio.Sample{CH: false, HW: false}, 4)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.Events) != 0 {
		t.Errorf("expected 0 heating events, got %d", len(pub.Events))
	}

	// Should have exactly one system event: SHUTDOWN
	if len(pub.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(pub.SystemEvents))
	}
	if pub.SystemEvents[0].Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN event, got %q", pub.SystemEvents[0].Event)
	}
}

func TestRunLoopSingleTransition(t *testing.T) {
	// 4× baseline (both off) + 4× CH on → should produce 1 CH_ON event
	samples := append(
		repeat(gpio.Sample{CH: false, HW: false}, 4),
		repeat(gpio.Sample{CH: true, HW: false}, 4)...,
	)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.Events) != 1 {
		t.Fatalf("expected 1 heating event, got %d", len(pub.Events))
	}
	if pub.Events[0].Type != "CH_ON" {
		t.Errorf("expected CH_ON, got %s", pub.Events[0].Type)
	}
	if pub.Events[0].CHState != "ON" {
		t.Errorf("expected CH state ON, got %s", pub.Events[0].CHState)
	}
	if pub.Events[0].HWState != "OFF" {
		t.Errorf("expected HW state OFF, got %s", pub.Events[0].HWState)
	}
}

func TestRunLoopMultipleTransitions(t *testing.T) {
	// baseline → CH on → HW on → CH off
	samples := append(
		repeat(gpio.Sample{CH: false, HW: false}, 4), // baseline
		append(
			repeat(gpio.Sample{CH: true, HW: false}, 4), // CH turns on
			append(
				repeat(gpio.Sample{CH: true, HW: true}, 4), // HW turns on
				repeat(gpio.Sample{CH: false, HW: true}, 4)..., // CH turns off
			)...,
		)...,
	)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.Events) != 3 {
		t.Fatalf("expected 3 heating events, got %d", len(pub.Events))
	}

	wantTypes := []string{"CH_ON", "HW_ON", "CH_OFF"}
	for i, want := range wantTypes {
		if string(pub.Events[i].Type) != want {
			t.Errorf("event %d: expected %s, got %s", i, want, pub.Events[i].Type)
		}
	}
}

func TestRunLoopBounceRejection(t *testing.T) {
	// baseline + 1× bounce (CH on) + return to baseline
	// The single bounce sample is shorter than debounce, so no event should fire.
	samples := append(
		repeat(gpio.Sample{CH: false, HW: false}, 4), // baseline
		append(
			[]gpio.Sample{{CH: true, HW: false}},                // 1× bounce
			repeat(gpio.Sample{CH: false, HW: false}, 4)..., // return to stable
		)...,
	)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.Events) != 0 {
		t.Errorf("expected 0 heating events (bounce rejected), got %d", len(pub.Events))
	}
}

func TestRunLoopGPIOReadError(t *testing.T) {
	// Provide some valid samples, then set ReadError to simulate failure.
	// The loop should continue past errors and still publish SHUTDOWN.
	reader := &gpio.FakeReader{
		Samples: repeat(gpio.Sample{CH: false, HW: false}, 2),
	}

	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	tick := make(chan time.Time)
	sig := make(chan os.Signal, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runLoop(reader, pub, 250*time.Millisecond, 0, clock, tick, sig)
	}()

	// Send 2 valid ticks
	tick <- time.Time{}
	tick <- time.Time{}

	// Now set read error and send more ticks — loop should not crash
	reader.ReadError = errors.New("gpio fault")
	tick <- time.Time{}
	tick <- time.Time{}

	sig <- syscall.SIGTERM

	err := <-errCh
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	// SHUTDOWN should still be published
	found := false
	for _, se := range pub.SystemEvents {
		if se.Event == "SHUTDOWN" {
			found = true
		}
	}
	if !found {
		t.Error("expected SHUTDOWN system event after GPIO errors")
	}
}

func TestRunLoopHeartbeat(t *testing.T) {
	// Use a 5-minute clock step so the heartbeat interval (15 min) triggers
	// after baseline is established.
	// With 100ms debounce step would be too slow. Use 5-min step with 5-min debounce.
	// Actually: let's use a large step to make heartbeat fire.
	// 4 ticks to baseline at 5-min step = 20 min elapsed. With 15-min heartbeat,
	// the first heartbeat check after baseline should fire.
	step := 5 * time.Minute
	debounce := 10 * time.Minute // needs 3+ ticks (15 min elapsed from first sample)
	heartbeatInterval := 15 * time.Minute

	// 4 ticks at 5-min step: clock calls are t0, t1(+5m), t2(+10m), t3(+15m), t4(+20m)
	// t0 = startTime. t1..t4 = ticks.
	// Baseline: first sample at t1 starts pending. By t4 (20m-5m=15m >= 10m debounce), baselines.
	// After baseline, CheckHeartbeat(t4, 15m): t4-t0 = 20m >= 15m → fires.
	samples := repeat(gpio.Sample{CH: false, HW: false}, 4)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), step)

	err := runRunLoop(t, reader, pub, debounce, heartbeatInterval, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	// Should have HEARTBEAT and SHUTDOWN system events
	var heartbeats, shutdowns int
	for _, se := range pub.SystemEvents {
		switch se.Event {
		case "HEARTBEAT":
			heartbeats++
			if se.Heartbeat == nil {
				t.Fatal("HEARTBEAT event missing heartbeat info")
			}
			if se.Heartbeat.UptimeSeconds <= 0 {
				t.Errorf("expected positive uptime, got %d", se.Heartbeat.UptimeSeconds)
			}
		case "SHUTDOWN":
			shutdowns++
		}
	}
	if heartbeats != 1 {
		t.Errorf("expected 1 HEARTBEAT event, got %d", heartbeats)
	}
	if shutdowns != 1 {
		t.Errorf("expected 1 SHUTDOWN event, got %d", shutdowns)
	}
}

func TestRunLoopPublishError(t *testing.T) {
	// A transition occurs but Publish returns an error — loop should continue.
	samples := append(
		repeat(gpio.Sample{CH: false, HW: false}, 4),
		repeat(gpio.Sample{CH: true, HW: false}, 4)...,
	)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	pub.PublishError = fmt.Errorf("broker unavailable")
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	// Heating events should not be recorded (PublishError causes Publish to return error
	// without recording), but SHUTDOWN should still be published via PublishSystem.
	if len(pub.Events) != 0 {
		t.Errorf("expected 0 recorded events (publish failed), got %d", len(pub.Events))
	}

	found := false
	for _, se := range pub.SystemEvents {
		if se.Event == "SHUTDOWN" {
			found = true
		}
	}
	if !found {
		t.Error("expected SHUTDOWN system event despite publish errors")
	}
}

func TestRunLoopShutdownSIGINT(t *testing.T) {
	samples := repeat(gpio.Sample{CH: false, HW: false}, 4)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGINT)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(pub.SystemEvents))
	}
	se := pub.SystemEvents[0]
	if se.Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN, got %q", se.Event)
	}
	if se.Reason != "SIGINT" {
		t.Errorf("expected reason SIGINT, got %q", se.Reason)
	}
	if se.Retained != true {
		t.Error("expected Retained=true for SHUTDOWN")
	}
}

func TestRunLoopShutdownSIGTERM(t *testing.T) {
	samples := repeat(gpio.Sample{CH: false, HW: false}, 4)
	reader := gpio.NewFakeReader(samples)
	pub := mqtt.NewFakePublisher()
	clock := fakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100*time.Millisecond)

	err := runRunLoop(t, reader, pub, 250*time.Millisecond, 0, clock, len(samples), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	if len(pub.SystemEvents) != 1 {
		t.Fatalf("expected 1 system event, got %d", len(pub.SystemEvents))
	}
	se := pub.SystemEvents[0]
	if se.Event != "SHUTDOWN" {
		t.Errorf("expected SHUTDOWN, got %q", se.Event)
	}
	if se.Reason != "SIGTERM" {
		t.Errorf("expected reason SIGTERM, got %q", se.Reason)
	}
	if se.Retained != true {
		t.Error("expected Retained=true for SHUTDOWN")
	}
}
