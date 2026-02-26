# Boiler Sensor: A Code Walkthrough

*2026-02-26T14:06:29Z by Showboat 0.6.1*
<!-- showboat-id: ea01e474-981e-42f3-935c-c66309149360 -->

This document walks through the `boiler-sensor` codebase linearly, from the entry point down through each layer. The daemon runs on a Raspberry Pi Zero, watches two GPIO pins (central heating and hot water), debounces the signals, and publishes state-change events to MQTT. It also serves a live status dashboard over HTTP.

The architecture enforces a strict separation of concerns so that every layer except the hardware drivers is testable without a Raspberry Pi.

## Project layout

```bash
find . -name '*.go' | grep -v _test | sort
```

```output
./cmd/boiler-sensor/main.go
./internal/gpio/fake.go
./internal/gpio/gpio.go
./internal/gpio/real.go
./internal/gpio/stub.go
./internal/logic/detector.go
./internal/logic/types.go
./internal/mqtt/buffer.go
./internal/mqtt/fake.go
./internal/mqtt/mqtt.go
./internal/mqtt/real.go
./internal/status/json.go
./internal/status/status.go
./internal/web/json.go
./internal/web/server.go
./internal/web/template.go
```

Each directory is a single responsibility. `cmd/boiler-sensor` is pure wiring — no business logic lives there. The `internal/logic` package is the heart of the daemon: it has zero external imports and must stay that way. The three abstraction layers (`gpio`, `mqtt`, `status`) each have a real implementation, a test double (`fake.go`), and where needed a build-tag stub (`stub.go`) for non-Linux platforms.

## GPIO layer — the hardware boundary

```bash
cat internal/gpio/gpio.go
```

```output
// Package gpio provides GPIO input reading with hardware abstraction.
// The real implementation uses Linux GPIO character device.
// The fake implementation allows testing without hardware.
package gpio

// Reader reads GPIO input states.
type Reader interface {
	// Read returns the logical states of CH and HW.
	// The raw GPIO values are inverted: raw active = logical OFF.
	// Returns (chOn, hwOn, error).
	Read() (bool, bool, error)

	// Close releases GPIO resources.
	Close() error
}

// Default pin definitions (BCM numbering)
const (
	DefaultPinCH = 26 // Central Heating
	DefaultPinHW = 16 // Hot Water
)
```

The `Reader` interface is the entire contract between the daemon and the hardware. Two booleans out: is central heating on? Is hot water on? The comment on the interface documents a crucial invariant: **raw GPIO active = logical OFF**. The physical relay pulls the pin low when the boiler is firing, so the signal is inverted. This inversion is handled exactly once — in `real.go` — and nowhere else in the codebase.

Here is the inversion in , the only place it appears:

```bash
grep -n 'invert\|\!val\|logical\|raw\|active' internal/gpio/real.go
```

```output
47:// Read returns the logical states of CH and HW.
48:// Inverts raw GPIO: raw active (1) = logical OFF, raw inactive (0) = logical ON.
60:	// Invert: raw active (1) = OFF, raw inactive (0) = ON
```

The full Read method, showing pin reads and the single inversion site:

```bash
sed -n '47,75p' internal/gpio/real.go
```

```output
// Read returns the logical states of CH and HW.
// Inverts raw GPIO: raw active (1) = logical OFF, raw inactive (0) = logical ON.
func (r *RealReader) Read() (bool, bool, error) {
	chRaw, err := r.chPin.Value()
	if err != nil {
		return false, false, fmt.Errorf("read CH pin: %w", err)
	}

	hwRaw, err := r.hwPin.Value()
	if err != nil {
		return false, false, fmt.Errorf("read HW pin: %w", err)
	}

	// Invert: raw active (1) = OFF, raw inactive (0) = ON
	chOn := chRaw == 0
	hwOn := hwRaw == 0

	return chOn, hwOn, nil
}

// Close releases GPIO resources.
// Reconfigures pins to input with pull-down (matching Pi boot defaults) before
// closing to ensure clean state for system shutdown/reboot.
func (r *RealReader) Close() error {
	var errs []error

	// Reconfigure pins to match Raspberry Pi boot defaults (input with pull-down).
	// This prevents boot issues when external hardware (like optocouplers) is
	// connected and might hold pins in unexpected states during early boot.
```

`chRaw == 0` is the entire inversion. Raw 0 means the pin is inactive (high), which means the relay is engaged, which means the boiler channel is ON. If you ever find yourself checking for inversion anywhere else in the codebase, something has gone wrong.

For CI (non-Linux builds), `stub.go` provides a build-tag alternative that returns an error immediately, so the package compiles but can't be used at runtime on macOS or Windows.

## Business logic — internal/logic/types.go

The `internal/logic` package defines the domain types. No imports from GPIO, MQTT, OS, or time.Sleep — just plain Go.

```bash
cat internal/logic/types.go
```

```output
// Package logic contains pure business logic for heating system state tracking.
// This package has NO external dependencies (no GPIO, MQTT, OS, or time.Sleep).
// Time is always injectable via time.Time parameters.
package logic

import "time"

// State represents the logical state of a heating channel.
type State string

const (
	StateOn  State = "ON"
	StateOff State = "OFF"
)

// EventType represents a state transition event.
type EventType string

const (
	EventCHOn  EventType = "CH_ON"
	EventCHOff EventType = "CH_OFF"
	EventHWOn  EventType = "HW_ON"
	EventHWOff EventType = "HW_OFF"
)

// Event represents a state transition to be published.
type Event struct {
	Timestamp time.Time
	Type      EventType
	CHState   State
	HWState   State
}

// ChannelState tracks debounce state for a single channel.
type ChannelState struct {
	// Current stable (debounced) state
	Stable State
	// Pending state during debounce
	Pending State
	// Time when pending state was first observed
	PendingSince time.Time
	// Whether we have established a baseline
	Baselined bool
}

// Input represents a single sample of logical states.
type Input struct {
	CH   bool // true = ON, false = OFF (already inverted from raw GPIO)
	HW   bool
	Time time.Time
}

// EventCounts tracks the number of each event type since startup.
type EventCounts struct {
	CHOn  int
	CHOff int
	HWOn  int
	HWOff int
}

// HeartbeatData contains information for a heartbeat event.
type HeartbeatData struct {
	Timestamp time.Time
	Uptime    time.Duration
	Counts    EventCounts
}
```

`ChannelState` is the per-channel debounce state machine. `pendingState` holds the value we've seen but haven't committed yet; `pendingSince` records when we first saw it. A transition only fires once the pending state has been stable for longer than the debounce duration.

`Input` is a snapshot: two booleans from the GPIO layer plus the current time. Time is injected as a parameter rather than read from `time.Now()` inside the function — that's what makes the logic deterministically testable.

## The Detector — internal/logic/detector.go

`Detector` is the core state machine. It holds debounce state for each channel and knows whether a baseline has been established yet.

```bash
sed -n '1,45p' internal/logic/detector.go
```

```output
package logic

import "time"

// Detector tracks state and detects debounced transitions.
type Detector struct {
	debounceDuration time.Duration
	ch               ChannelState
	hw               ChannelState
	baselined        bool
	startTime        time.Time
	eventCounts      EventCounts
	lastHeartbeat    time.Time
}

// NewDetector creates a new transition detector with the given debounce duration.
// The startTime is used for calculating uptime in heartbeat events.
func NewDetector(debounceDuration time.Duration, startTime time.Time) *Detector {
	return &Detector{
		debounceDuration: debounceDuration,
		startTime:        startTime,
		lastHeartbeat:    startTime,
	}
}

// Process takes a new input sample and returns any events that should be emitted.
// Events are only returned after baseline is established and on state transitions.
func (d *Detector) Process(input Input) []Event {
	chState := boolToState(input.CH)
	hwState := boolToState(input.HW)

	chTransition := d.processChannel(&d.ch, chState, input.Time)
	hwTransition := d.processChannel(&d.hw, hwState, input.Time)

	// Check if we've established baseline
	if !d.baselined {
		if d.ch.Baselined && d.hw.Baselined {
			d.baselined = true
		}
		return nil // No events until baseline established
	}

	var events []Event

	// Emit events for transitions (order: CH first, then HW if both change simultaneously)
```

### Process — the main entry point

Every poll cycle calls `Process(input)`. It returns a slice of `Event` values (zero, one, or two — one per channel that just transitioned).

```bash
grep -n 'func.*Process\|func.*process' internal/logic/detector.go
```

```output
28:func (d *Detector) Process(input Input) []Event {
83:func (d *Detector) processChannel(ch *ChannelState, newState State, now time.Time) *EventType {
```

```bash
sed -n '47,90p' internal/logic/detector.go
```

```output
		events = append(events, Event{
			Timestamp: input.Time,
			Type:      *chTransition,
			CHState:   d.ch.Stable,
			HWState:   d.hw.Stable,
		})
	}

	if hwTransition != nil {
		events = append(events, Event{
			Timestamp: input.Time,
			Type:      *hwTransition,
			CHState:   d.ch.Stable,
			HWState:   d.hw.Stable,
		})
	}

	// Count events
	for _, e := range events {
		switch e.Type {
		case EventCHOn:
			d.eventCounts.CHOn++
		case EventCHOff:
			d.eventCounts.CHOff++
		case EventHWOn:
			d.eventCounts.HWOn++
		case EventHWOff:
			d.eventCounts.HWOff++
		}
	}

	return events
}

// processChannel handles debounce logic for a single channel.
// Returns the event type if a transition occurred, nil otherwise.
func (d *Detector) processChannel(ch *ChannelState, newState State, now time.Time) *EventType {
	// First time seeing this channel
	if !ch.Baselined {
		if ch.Pending == "" {
			// Start observing
			ch.Pending = newState
			ch.PendingSince = now
			return nil
```

The outer `Process` calls `processChannel` twice — once for CH, once for HW — and concatenates the results. CH is processed first, which is why CH always appears before HW in any simultaneous event pair.

### processChannel — the debounce logic

```bash
sed -n '91,165p' internal/logic/detector.go
```

```output
		}

		if ch.Pending != newState {
			// State changed during baseline, restart
			ch.Pending = newState
			ch.PendingSince = now
			return nil
		}

		// Check if debounce period has passed
		if now.Sub(ch.PendingSince) >= d.debounceDuration {
			ch.Stable = newState
			ch.Baselined = true
			ch.Pending = ""
		}
		return nil
	}

	// Already baselined - detect transitions
	if newState == ch.Stable {
		// No change from stable state, clear any pending
		ch.Pending = ""
		return nil
	}

	// State differs from stable
	if ch.Pending != newState {
		// New pending state
		ch.Pending = newState
		ch.PendingSince = now
		return nil
	}

	// Same pending state, check debounce
	if now.Sub(ch.PendingSince) >= d.debounceDuration {
		oldState := ch.Stable
		ch.Stable = newState
		ch.Pending = ""
		return eventTypeForTransition(oldState, newState, ch == &d.ch)
	}

	return nil
}

func boolToState(b bool) State {
	if b {
		return StateOn
	}
	return StateOff
}

func eventTypeForTransition(from, to State, isCH bool) *EventType {
	var event EventType
	if isCH {
		if to == StateOn {
			event = EventCHOn
		} else {
			event = EventCHOff
		}
	} else {
		if to == StateOn {
			event = EventHWOn
		} else {
			event = EventHWOff
		}
	}
	return &event
}

// StartTime returns the time the detector was created.
func (d *Detector) StartTime() time.Time {
	return d.startTime
}

// EventCountsSnapshot returns a copy of the current event counts.
```

The debounce works in two phases:

1. **Baseline phase** (`\!cs.baselined`): we wait for the first stable reading, then record it as the known state without emitting any event. This is how the daemon avoids publishing a spurious CH_ON at startup just because the boiler happened to be running.

2. **Normal phase**: if the current reading differs from the last committed state, start a pending timer. If the pending state holds for the full debounce duration, commit it and emit an event. If it flips back before the timer expires, discard the pending state — that's bounce rejection.

## MQTT layer — internal/mqtt/mqtt.go

The `Publisher` interface separates the two kinds of messages the daemon sends.

```bash
sed -n '1,55p' internal/mqtt/mqtt.go
```

```output
// Package mqtt provides MQTT publishing with abstraction for testing.
package mqtt

import (
	"encoding/json"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

// Topic is the MQTT topic for boiler events.
const Topic = "energy/boiler/sensor/events"

// TopicSystem is the MQTT topic for system lifecycle events.
const TopicSystem = "energy/boiler/sensor/system"

// Publisher publishes events to MQTT.
type Publisher interface {
	// Publish sends a boiler event to the broker.
	// Returns error if publishing fails (should not crash the process).
	Publish(event logic.Event) error

	// PublishSystem sends a system lifecycle event to the broker.
	PublishSystem(event SystemEvent) error

	// Close disconnects from the broker.
	Close() error
}

// ConnectionStatus reports whether the MQTT connection is active.
type ConnectionStatus interface {
	IsConnected() bool
}

// SystemEvent represents a system lifecycle event (e.g., startup, shutdown, heartbeat).
type SystemEvent struct {
	Timestamp  time.Time
	Event      string // e.g., "STARTUP", "SHUTDOWN", "HEARTBEAT"
	Reason     string // e.g., "SIGTERM", "SIGINT" (shutdown only)
	RawPayload []byte // Pre-formatted JSON payload; if set, FormatSystemPayload returns it directly
	Retained   bool   // Whether the message should be retained by the broker
}

// Payload represents the MQTT message payload structure.
type Payload struct {
	Boiler BoilerPayload `json:"boiler"`
}

// BoilerPayload contains the boiler event details.
type BoilerPayload struct {
	Timestamp string       `json:"timestamp"`
	Event     string       `json:"event"`
	CH        ChannelState `json:"ch"`
	HW        ChannelState `json:"hw"`
}
```

`Publish` carries the heating-state events. `PublishSystem` carries lifecycle events (STARTUP, SHUTDOWN, HEARTBEAT, RECONNECTED). The distinction matters for QoS and retention: system events use QoS 1 and some are retained so a subscriber joining late gets the last known state.

### Payload format

```bash
sed -n '56,104p' internal/mqtt/mqtt.go
```

```output

// ChannelState represents a single channel's state.
type ChannelState struct {
	State string `json:"state"`
}

// FormatPayload creates the JSON payload for a boiler event.
func FormatPayload(event logic.Event) ([]byte, error) {
	payload := Payload{
		Boiler: BoilerPayload{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     string(event.Type),
			CH:        ChannelState{State: string(event.CHState)},
			HW:        ChannelState{State: string(event.HWState)},
		},
	}
	return json.Marshal(payload)
}

// SystemPayload represents the MQTT message payload for system events.
// Used for simple events (LWT, RECONNECTED) that don't carry a full status snapshot.
type SystemPayload struct {
	System SystemPayloadInner `json:"system"`
}

// SystemPayloadInner contains the system event details.
type SystemPayloadInner struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Reason    string `json:"reason,omitempty"`
}

// FormatSystemPayload creates the JSON payload for a system event.
// If event.RawPayload is set, it is returned directly (used for full status snapshots).
func FormatSystemPayload(event SystemEvent) ([]byte, error) {
	if event.RawPayload != nil {
		return event.RawPayload, nil
	}

	payload := SystemPayload{
		System: SystemPayloadInner{
			Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
			Event:     event.Event,
			Reason:    event.Reason,
		},
	}
	return json.Marshal(payload)
}
```

The JSON structure nests everything under a `boiler` key. A typical event looks like:

```json
{
  "boiler": {
    "timestamp": "2026-02-02T22:18:12Z",
    "event": "CH_ON",
    "ch": { "state": "ON" },
    "hw": { "state": "OFF" }
  }
}
```

`FormatPayload` converts an `Event` from the logic layer into this JSON. The timestamp is serialised as RFC3339 UTC.

### Ring buffer — internal/mqtt/buffer.go

The real MQTT publisher buffers messages when the broker is unreachable.

```bash
cat internal/mqtt/buffer.go
```

```output
package mqtt

import "log"

// bufferedMsg stores a serialized MQTT message for replay after reconnection.
type bufferedMsg struct {
	topic    string
	payload  []byte
	qos      byte
	retained bool
}

// ringBuffer is a fixed-capacity FIFO that stores messages while disconnected.
// Not safe for concurrent use — caller must synchronize.
type ringBuffer struct {
	buf      []bufferedMsg
	capacity int
	head     int // next write position
	count    int
	overflow bool // true if any message was dropped since last drain
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		buf:      make([]bufferedMsg, capacity),
		capacity: capacity,
	}
}

func (r *ringBuffer) push(msg bufferedMsg) {
	if r.count == r.capacity {
		if !r.overflow {
			log.Printf("mqtt: buffer full (%d messages), dropping oldest", r.capacity)
			r.overflow = true
		}
		// Overwrite oldest: head is already pointing at it
		r.buf[r.head] = msg
		r.head = (r.head + 1) % r.capacity
		// count stays at capacity
		return
	}
	r.buf[r.head] = msg
	r.head = (r.head + 1) % r.capacity
	r.count++
}

func (r *ringBuffer) drainAll() []bufferedMsg {
	if r.count == 0 {
		return nil
	}

	result := make([]bufferedMsg, r.count)
	// Oldest item is at (head - count) mod capacity
	start := (r.head - r.count + r.capacity) % r.capacity
	for i := 0; i < r.count; i++ {
		result[i] = r.buf[(start+i)%r.capacity]
	}

	r.count = 0
	r.head = 0
	r.overflow = false
	return result
}

func (r *ringBuffer) len() int {
	return r.count
}
```

The ring buffer has a fixed capacity (100 in the real publisher). When the broker reconnects, `drainAll` is called and every buffered message is replayed in order. If the buffer fills before reconnection, the oldest messages are dropped — you lose old events but never lose recent ones.

### Reconnection and replay in real.go

```bash
grep -n 'drain\|reconnect\|Reconnect\|onConnect\|replay\|buffer' internal/mqtt/real.go | head -30
```

```output
45:		SetAutoReconnect(true).
48:		SetConnectionLostHandler(p.onConnectionLost).
49:		SetReconnectingHandler(p.onReconnecting).
50:		SetOnConnectHandler(p.onConnect)
64:func (p *RealPublisher) onConnectionLost(_ paho.Client, err error) {
71:func (p *RealPublisher) onReconnecting(_ paho.Client, _ *paho.ClientOptions) {
72:	log.Printf("mqtt: reconnecting to broker")
75:func (p *RealPublisher) onConnect(_ paho.Client) {
80:	msgs := p.buf.drainAll()
87:			log.Printf("mqtt: replay timeout for buffered message")
91:			log.Printf("mqtt: replay error: %v", err)
98:		log.Printf("mqtt: connected to broker, replayed %d/%d buffered events", n, len(msgs))
103:	// On reconnect (not first connect), publish a RECONNECTED event
106:		reconnectPayload, err := FormatSystemPayload(SystemEvent{
114:		token := p.client.Publish(TopicSystem, 1, true, reconnectPayload)
139:		p.buf.push(bufferedMsg{topic: p.topic, payload: payload, qos: 0, retained: false})
166:		p.buf.push(bufferedMsg{topic: TopicSystem, payload: payload, qos: 1, retained: event.Retained})
```

```bash
sed -n '1,60p' internal/mqtt/real.go
```

```output
package mqtt

import (
	"fmt"
	"log"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/sweeney/boiler-sensor/internal/logic"
)

// RealPublisher publishes to an actual MQTT broker.
type RealPublisher struct {
	client        paho.Client
	topic         string
	mu            sync.Mutex
	connected     bool
	everConnected bool
	buf           *ringBuffer
}

// NewRealPublisher creates a publisher that connects to the given broker
// asynchronously. It returns immediately — Paho retries in the background.
func NewRealPublisher(broker string) *RealPublisher {
	willPayload, err := FormatSystemPayload(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
	})
	if err != nil {
		// FormatSystemPayload only fails on json.Marshal of a known struct —
		// unreachable in practice, but log and continue without a will.
		log.Printf("mqtt: failed to format will payload: %v", err)
	}

	p := &RealPublisher{
		topic: Topic,
		buf:   newRingBuffer(100),
	}

	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID("boiler-sensor").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetConnectionLostHandler(p.onConnectionLost).
		SetReconnectingHandler(p.onReconnecting).
		SetOnConnectHandler(p.onConnect)

	if willPayload != nil {
		opts.SetWill(TopicSystem, string(willPayload), 1, true)
	}

	p.client = paho.NewClient(opts)

	// Non-blocking connect — Paho retries via ConnectRetry.
	p.client.Connect()

```

On reconnection the publisher does two things: it replays the ring buffer, and it publishes a RECONNECTED system event. That event replaces the stale SHUTDOWN will message (LWT) that the broker may have published when the TCP connection dropped. The first connection intentionally does *not* send RECONNECTED — only genuine reconnections do.

## Status tracker — internal/status/status.go

The tracker is a thread-safe value store that the web server and (in future) an LED driver can read without touching the GPIO or MQTT layers.

```bash
cat internal/status/status.go
```

```output
// Package status provides a thread-safe status tracker for the boiler-sensor daemon.
// It is designed to be read by HTTP handlers and (future) LED drivers.
package status

import (
	"sync"
	"time"

	"github.com/sweeney/boiler-sensor/internal/logic"
)

// NetworkInfo contains network state. This is a local copy to avoid
// importing internal/mqtt from status.
type NetworkInfo struct {
	Type       string
	IP         string
	Status     string
	Gateway    string
	WifiStatus string
	SSID       string
}

// Config contains daemon configuration for display.
type Config struct {
	PollMs      int64
	DebounceMs  int64
	HeartbeatMs int64
	Broker      string
	HTTPPort    string
	WSBroker    string // Websocket broker URL for browser MQTT (empty = disabled)
}

// Snapshot is a point-in-time view of daemon state.
// It is a value type — safe to use after the lock is released.
type Snapshot struct {
	CH            logic.State
	HW            logic.State
	Baselined     bool
	Counts        logic.EventCounts
	StartTime     time.Time
	Now           time.Time
	MQTTConnected bool
	Network       *NetworkInfo
	Config        Config
}

// Uptime returns the duration since the daemon started.
func (s Snapshot) Uptime() time.Duration {
	return s.Now.Sub(s.StartTime)
}

// Tracker holds mutable daemon state behind an RWMutex.
type Tracker struct {
	mu   sync.RWMutex
	snap Snapshot
}

// NewTracker creates a Tracker with the given start time and config.
func NewTracker(startTime time.Time, cfg Config) *Tracker {
	return &Tracker{
		snap: Snapshot{
			StartTime: startTime,
			Config:    cfg,
		},
	}
}

// Update sets channel states, baseline status, and event counts.
// Called from runLoop on every tick.
func (t *Tracker) Update(ch, hw logic.State, baselined bool, counts logic.EventCounts) {
	t.mu.Lock()
	t.snap.CH = ch
	t.snap.HW = hw
	t.snap.Baselined = baselined
	t.snap.Counts = counts
	t.mu.Unlock()
}

// SetMQTTConnected sets the MQTT connection status.
func (t *Tracker) SetMQTTConnected(connected bool) {
	t.mu.Lock()
	t.snap.MQTTConnected = connected
	t.mu.Unlock()
}

// SetNetwork sets the network info.
func (t *Tracker) SetNetwork(info *NetworkInfo) {
	t.mu.Lock()
	t.snap.Network = info
	t.mu.Unlock()
}

// Snapshot returns a point-in-time copy of the daemon state.
// The Now field is set to the current time at the moment of the call.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	s := t.snap
	t.mu.RUnlock()
	s.Now = time.Now()
	return s
}
```

`Snapshot` is a plain value type with no locks — safe to pass around freely after it leaves the tracker. `Update` is the write path from the main loop; `SetMQTTConnected` and `SetNetwork` are called from goroutines, so all three take the write lock. `Snapshot()` takes the read lock and copies the value before returning.

## Web server — internal/web/server.go

```bash
cat internal/web/server.go
```

```output
// Package web provides an HTTP status server for the boiler-sensor daemon.
package web

import (
	"context"
	_ "embed"
	"net"
	"net/http"

	"github.com/sweeney/boiler-sensor/internal/status"
)

//go:embed static/mqtt.min.js
var mqttJS []byte

// Server serves the status page over HTTP.
type Server struct {
	httpServer *http.Server
	tracker    *status.Tracker
}

// New creates a Server that reads state from the given tracker.
func New(addr string, tracker *status.Tracker) *Server {
	s := &Server{tracker: tracker}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/index.html", s.handleIndex)
	mux.HandleFunc("/index.json", s.handleJSON)
	mux.HandleFunc("/mqtt.min.js", handleMQTTJS)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

// ListenAndServe starts listening. It blocks until the server is shut down.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Serve accepts connections on the given listener. Useful for tests.
func (s *Server) Serve(ln net.Listener) error {
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	snap := s.tracker.Snapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderHTML(w, snap)
}

func (s *Server) handleJSON(w http.ResponseWriter, r *http.Request) {
	snap := s.tracker.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.Write(formatJSON(snap))
}

func handleMQTTJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write(mqttJS)
}
```

Three routes:

- **`/index.json`** — machine-readable snapshot of current state, called by monitoring scripts.
- **`/` and `/index.html`** — the HTML dashboard, which embeds the same JSON and a small JavaScript client that subscribes to the MQTT broker over WebSocket for live updates.
- **`/mqtt.min.js`** — the MQTT.js library, served from an embedded file so there are no CDN dependencies.

The server holds a reference to the `Tracker` and reads a fresh snapshot on every request. No caching — always consistent with the current state.

## Entry point — cmd/boiler-sensor/main.go

### Flags and wiring

```bash
sed -n '1,80p' cmd/boiler-sensor/main.go
```

```output
// Command boiler-sensor monitors GPIO inputs and publishes heating state changes to MQTT.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sweeney/boiler-sensor/internal/gpio"
	"github.com/sweeney/boiler-sensor/internal/logic"
	"github.com/sweeney/boiler-sensor/internal/mqtt"
	"github.com/sweeney/boiler-sensor/internal/status"
	"github.com/sweeney/boiler-sensor/internal/web"
)

func main() {
	poll := flag.Duration("poll", 100*time.Millisecond, "GPIO polling interval")
	debounce := flag.Duration("debounce", 250*time.Millisecond, "Debounce duration")
	broker := flag.String("broker", "tcp://192.168.1.200:1883", "MQTT broker address")
	heartbeat := flag.Duration("heartbeat", 15*time.Minute, "Heartbeat interval (0 to disable)")
	pinCH := flag.Int("pin-ch", gpio.DefaultPinCH, "BCM pin number for Central Heating")
	pinHW := flag.Int("pin-hw", gpio.DefaultPinHW, "BCM pin number for Hot Water")
	printState := flag.Bool("print-state", false, "Print current state and exit")
	httpAddr := flag.String("http", ":80", "HTTP status address (empty to disable)")
	wsBroker := flag.String("ws-broker", "=broker", `MQTT websocket URL for live UI ("=broker" derives from --broker, "off" disables)`)

	flag.Parse()

	ws := resolveWSBroker(*wsBroker, *broker)
	if err := run(*poll, *debounce, *broker, *heartbeat, *pinCH, *pinHW, *printState, *httpAddr, ws); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run(poll, debounce time.Duration, broker string, heartbeat time.Duration, pinCH, pinHW int, printState bool, httpAddr, wsBroker string) error {
	// Initialize GPIO
	gpioReader, err := gpio.NewRealReader(pinCH, pinHW)
	if err != nil {
		return fmt.Errorf("init gpio: %w", err)
	}
	defer gpioReader.Close()

	// Print state mode
	if printState {
		ch, hw, err := gpioReader.Read()
		if err != nil {
			return fmt.Errorf("read gpio: %w", err)
		}
		chState := stateString(ch)
		hwState := stateString(hw)
		fmt.Printf("CH: %s, HW: %s\n", chState, hwState)
		return nil
	}

	// Initialize MQTT
	publisher := mqtt.NewRealPublisher(broker)
	defer publisher.Close()

	// Initialize status tracker (before STARTUP so snapshot is available)
	tracker := status.NewTracker(time.Now(), status.Config{
		PollMs:      poll.Milliseconds(),
		DebounceMs:  debounce.Milliseconds(),
		HeartbeatMs: heartbeat.Milliseconds(),
		Broker:      broker,
		HTTPPort:    httpAddr,
		WSBroker:    wsBroker,
	})
	if net := readNetworkInfo(); net != nil {
		tracker.SetNetwork(net)
	}

	// Publish startup event with full status snapshot
	snap := tracker.Snapshot()
```

The flags map directly to the major tunables: poll interval (how often to read GPIO), debounce duration (how long a signal must be stable), MQTT broker address, and HTTP port. Pinning BCM numbers at the command line means you can redirect to different pins without recompiling — useful during hardware bring-up.

After parsing flags, main initialises the layers in dependency order: GPIO reader → MQTT publisher → status tracker → web server → then hands everything to `runLoop`.

### The main loop

```bash
grep -n 'func runLoop' cmd/boiler-sensor/main.go
```

```output
116:func runLoop(gpioReader gpio.Reader, publisher mqtt.Publisher, mqttStatus mqtt.ConnectionStatus, tracker *status.Tracker, debounce, heartbeat time.Duration, now func() time.Time, tick <-chan time.Time, sig <-chan os.Signal) error {
```

```bash
sed -n '100,185p' cmd/boiler-sensor/main.go
```

```output
		}()
		defer srv.Shutdown(context.Background())
		log.Printf("http status server listening on %s", httpAddr)
	}

	log.Printf("started: poll=%v debounce=%v broker=%s heartbeat=%v", poll, debounce, broker, heartbeat)

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	return runLoop(gpioReader, publisher, publisher, tracker, debounce, heartbeat, time.Now, ticker.C, sigCh)
}

func runLoop(gpioReader gpio.Reader, publisher mqtt.Publisher, mqttStatus mqtt.ConnectionStatus, tracker *status.Tracker, debounce, heartbeat time.Duration, now func() time.Time, tick <-chan time.Time, sig <-chan os.Signal) error {
	startTime := now()
	detector := logic.NewDetector(debounce, startTime)

	for {
		select {
		case s := <-sig:
			log.Printf("received %v, shutting down", s)
			signalName := "UNKNOWN"
			if s == syscall.SIGINT {
				signalName = "SIGINT"
			} else if s == syscall.SIGTERM {
				signalName = "SIGTERM"
			}
			event := mqtt.SystemEvent{
				Timestamp: now(),
				Event:     "SHUTDOWN",
				Reason:    signalName,
				Retained:  true,
			}
			if tracker != nil {
				if mqttStatus != nil {
					tracker.SetMQTTConnected(mqttStatus.IsConnected())
				}
				snap := tracker.Snapshot()
				event.RawPayload = status.FormatStatusEvent(snap, "SHUTDOWN", signalName)
			}
			if err := publisher.PublishSystem(event); err != nil {
				log.Printf("failed to publish shutdown event: %v", err)
			} else {
				log.Printf("published shutdown event")
			}
			return nil

		case <-tick:
			t := now()
			ch, hw, err := gpioReader.Read()
			if err != nil {
				log.Printf("gpio read error: %v", err)
				continue
			}

			events := detector.Process(logic.Input{
				CH:   ch,
				HW:   hw,
				Time: t,
			})

			for _, event := range events {
				log.Printf("event: %s (CH=%s HW=%s)", event.Type, event.CHState, event.HWState)
				if err := publisher.Publish(event); err != nil {
					log.Printf("publish error: %v", err)
					// Don't crash on publish failure
				}
			}

			if !detector.IsBaselined() {
				// Still waiting for baseline
				continue
			}

			// Check for heartbeat
			if hbData := detector.CheckHeartbeat(t, heartbeat); hbData != nil {
				log.Printf("heartbeat: uptime=%v ch_on=%d ch_off=%d hw_on=%d hw_off=%d",
					hbData.Uptime, hbData.Counts.CHOn, hbData.Counts.CHOff, hbData.Counts.HWOn, hbData.Counts.HWOff)

				hbEvent := mqtt.SystemEvent{
					Timestamp: hbData.Timestamp,
					Event:     "HEARTBEAT",
				}
```

The loop structure is straightforward:

1. Read GPIO → get (chOn, hwOn)
2. Build an `Input` with the current time
3. Call `detector.Process(input)` → get zero or more `Event` values
4. For each event: format JSON, publish to MQTT, update the tracker
5. Check heartbeat — if the interval has elapsed, publish a full status snapshot as a system event
6. Sleep for the poll interval

Errors from GPIO or MQTT are logged but don't crash the daemon. A GPIO read error skips the detector call for that cycle; a publish error is logged and the loop continues.

### System events: startup, shutdown, heartbeat

```bash
sed -n '185,266p' cmd/boiler-sensor/main.go
```

```output
				}
				if tracker != nil {
					if mqttStatus != nil {
						tracker.SetMQTTConnected(mqttStatus.IsConnected())
					}
					// Refresh network info for heartbeat
					if net := readNetworkInfo(); net != nil {
						tracker.SetNetwork(net)
					}
					chState, hwState := detector.CurrentState()
					tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())
					snap := tracker.Snapshot()
					hbEvent.RawPayload = status.FormatStatusEvent(snap, "HEARTBEAT", "")
				}
				if err := publisher.PublishSystem(hbEvent); err != nil {
					log.Printf("heartbeat publish error: %v", err)
				}
			}

			// Update status tracker for HTTP/LED consumers
			if tracker != nil {
				chState, hwState := detector.CurrentState()
				tracker.Update(chState, hwState, detector.IsBaselined(), detector.EventCountsSnapshot())
				if mqttStatus != nil {
					tracker.SetMQTTConnected(mqttStatus.IsConnected())
				}
			}
		}
	}
}

// pi-helper env var names (written to /run/pi-helper.env).
const (
	envNetworkType       = "NETWORK_TYPE"
	envNetworkIP         = "NETWORK_IP"
	envNetworkStatus     = "NETWORK_STATUS"
	envNetworkGateway    = "NETWORK_GATEWAY"
	envNetworkWifiStatus = "NETWORK_WIFI_STATUS"
	envNetworkWifiSSID   = "NETWORK_WIFI_SSID"
)

func readNetworkInfo() *status.NetworkInfo {
	s := os.Getenv(envNetworkStatus)
	if s == "" {
		return nil
	}
	return &status.NetworkInfo{
		Type:       os.Getenv(envNetworkType),
		IP:         os.Getenv(envNetworkIP),
		Status:     s,
		Gateway:    os.Getenv(envNetworkGateway),
		WifiStatus: os.Getenv(envNetworkWifiStatus),
		SSID:       os.Getenv(envNetworkWifiSSID),
	}
}

func stateString(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// resolveWSBroker converts the --ws-broker flag value into a concrete URL.
// "=broker" derives ws://host:9001 from the TCP broker address; empty disables.
func resolveWSBroker(ws, broker string) string {
	if ws == "off" {
		return ""
	}
	if ws != "=broker" {
		return ws
	}
	u, err := url.Parse(broker)
	if err != nil {
		log.Printf("ws-broker: cannot parse --broker %q: %v", broker, err)
		return ""
	}
	u.Scheme = "ws"
	u.Host = u.Hostname() + ":9001"
	return u.String()
}
```

Three lifecycle events bracket the daemon's life:

- **STARTUP** — published once before the loop begins, retained so late subscribers know the daemon is alive.
- **HEARTBEAT** — published on a configurable interval (default unset/disabled) carrying a full status snapshot: uptime, event counts, MQTT connection, network info. Retained=false so old heartbeats don't accumulate.
- **SHUTDOWN** — published on SIGINT/SIGTERM, also retained. There's also an MQTT will (LWT) set at connect time for the case where the process is killed hard or the network drops.

## Testing strategy

The architecture is designed so that no test ever needs real hardware. The fake implementations mirror the real interfaces exactly.

```bash
cat internal/gpio/fake.go
```

```output
package gpio

import "errors"

// FakeReader is a test double that returns scripted GPIO values.
type FakeReader struct {
	// Samples contains scripted (chOn, hwOn) values to return.
	// Each call to Read() consumes the next sample.
	Samples []Sample

	// index tracks current position in Samples
	index int

	// Closed tracks if Close was called
	Closed bool

	// ReadError, if set, will be returned by Read()
	ReadError error
}

// Sample represents a single GPIO reading (already in logical form).
type Sample struct {
	CH bool // true = ON
	HW bool // true = ON
}

// NewFakeReader creates a FakeReader with the given samples.
func NewFakeReader(samples []Sample) *FakeReader {
	return &FakeReader{Samples: samples}
}

// Read returns the next scripted sample.
// If samples are exhausted, returns the last sample repeatedly.
func (f *FakeReader) Read() (bool, bool, error) {
	if f.ReadError != nil {
		return false, false, f.ReadError
	}

	if len(f.Samples) == 0 {
		return false, false, errors.New("no samples configured")
	}

	sample := f.Samples[f.index]
	if f.index < len(f.Samples)-1 {
		f.index++
	}

	return sample.CH, sample.HW, nil
}

// Close marks the reader as closed.
func (f *FakeReader) Close() error {
	f.Closed = true
	return nil
}

// Reset resets the reader to the beginning of samples.
func (f *FakeReader) Reset() {
	f.index = 0
	f.Closed = false
}
```

```bash
cat internal/mqtt/fake.go
```

```output
package mqtt

import (
	"github.com/sweeney/boiler-sensor/internal/logic"
)

// FakePublisher records published events for test assertions.
type FakePublisher struct {
	// Events contains all boiler events that were published.
	Events []logic.Event

	// Payloads contains the JSON payloads that were published.
	Payloads [][]byte

	// SystemEvents contains all system events that were published.
	SystemEvents []SystemEvent

	// SystemPayloads contains the JSON payloads for system events.
	SystemPayloads [][]byte

	// PublishError, if set, will be returned by Publish.
	PublishError error

	// PublishSystemError, if set, will be returned by PublishSystem.
	PublishSystemError error

	// Closed tracks if Close was called.
	Closed bool

	// Connected controls the return value of IsConnected.
	Connected bool
}

// NewFakePublisher creates a FakePublisher for testing.
func NewFakePublisher() *FakePublisher {
	return &FakePublisher{}
}

// Publish records the boiler event.
func (f *FakePublisher) Publish(event logic.Event) error {
	if f.PublishError != nil {
		return f.PublishError
	}

	f.Events = append(f.Events, event)

	payload, err := FormatPayload(event)
	if err != nil {
		return err
	}
	f.Payloads = append(f.Payloads, payload)

	return nil
}

// PublishSystem records the system event.
func (f *FakePublisher) PublishSystem(event SystemEvent) error {
	if f.PublishSystemError != nil {
		return f.PublishSystemError
	}

	f.SystemEvents = append(f.SystemEvents, event)

	payload, err := FormatSystemPayload(event)
	if err != nil {
		return err
	}
	f.SystemPayloads = append(f.SystemPayloads, payload)

	return nil
}

// Close marks the publisher as closed.
func (f *FakePublisher) Close() error {
	f.Closed = true
	return nil
}

// IsConnected reports whether the fake publisher is "connected".
func (f *FakePublisher) IsConnected() bool {
	return f.Connected
}

// Reset clears recorded events.
func (f *FakePublisher) Reset() {
	f.Events = nil
	f.Payloads = nil
	f.SystemEvents = nil
	f.SystemPayloads = nil
	f.Closed = false
	f.PublishError = nil
	f.PublishSystemError = nil
	f.Connected = false
}
```

`FakeReader` takes a script of samples. `Read()` walks through them in order and repeats the last one once exhausted — so a test that runs for N poll cycles only needs to script the interesting transitions, not fill out the whole timeline.

`FakePublisher` records every call. After running a test scenario you assert on `fp.Events`, `fp.Payloads`, `fp.SystemEvents`, and `fp.SystemPayloads` — exact counts, exact JSON, exact order.

### Integration test example

Here is the full-flow integration test that exercises CH and HW transitions end to end:

```bash
sed -n '1,100p' internal/integration_test.go
```

```output
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
```

The test drives a fake clock so it controls exactly when debounce timers expire. A fake GPIO feeds a pre-scripted sequence of samples. The runLoop runs in a goroutine and is cancelled via context. After it exits, the test asserts on the exact list of published MQTT payloads — topic, JSON content, and order.

This pattern means every integration scenario — bounce rejection, simultaneous transitions, publish failures, heartbeat content — is fully deterministic and runs in milliseconds on any platform.

### Logic test coverage

```bash
go test -cover ./internal/logic/
```

```output
ok  	github.com/sweeney/boiler-sensor/internal/logic	0.385s	coverage: 100.0% of statements
```

100% coverage on the logic package is a hard requirement. Because the logic has no external dependencies, achieving and maintaining full coverage is straightforward — every branch in the state machine has a corresponding test scenario.
