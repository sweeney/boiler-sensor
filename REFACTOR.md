# Refactor plan: generalize the sensor engine, then extract `common/iot`

> **Status: complete; re-planned 2026-10-25.** All five steps are done. The
> engine lives in `github.com/sweeney/common/iot` at `iot/v0.1.0`, now published.
> `boiler-sensor` pins that tag and is the one live consumer. `doorbell-sensor`
> validated the API and is parked (deprioritised, not published). `iot` is scoped
> to GPIO on/off sensors; see `common/PLAN.md` for the re-plan. This document is
> kept as the record of why the split is shaped the way it is.

## Goal

Turn boiler-sensor's internals from a hardwired two-channel (CH + HW) heating
daemon into a **sensor-agnostic engine driven by a per-binary profile**, so a
second device (a doorbell-press sensor on a separate Pi) can reuse the engine
instead of forking it. The proven-generic engine is then lifted into a new
standalone module, `github.com/sweeney/common/iot`, consumed by both binaries.

## Guiding principle: generalize in place → validate with two consumers → lift

Do **not** extract to `common/iot` first. The packages are currently shaped for
two channels; extracting now would publish boiler-shaped code the doorbell can't
use, then break its API on first contact. Order of operations:

1. **Generalize in place** inside this repo. Boiler still works; tests stay green. ✅ done
2. **Lift** the now-generic packages into the importable module
   `github.com/sweeney/common/iot` (purely generic — no boiler/CH/HW names).
3. **Migrate `boiler-sensor` onto `common/iot`** (single source of truth — no
   engine duplication); the boiler profile stays in `cmd/boiler-sensor`.
4. **Build `sweeney/doorbell-sensor` as its own repo/module** importing
   `common/iot` — the second consumer that validates the API.
5. **Validate then tag.** Develop all three against a local `go.work`/`replace`
   directive; once the boiler golden is byte-identical and the doorbell golden is
   green, tag `iot/v0.1.0` and switch the consumers from `replace` to the tag.

> **Revised from the original "validate in-repo, then lift" ordering.** Each sensor
> is now its own deployable repo (the user's preference), and a separate repo cannot
> import another module's `internal/`. So the `common/iot` extraction moves *ahead*
> of the doorbell, and the doorbell validates `iot` as its own module. The
> "validate with two consumers before tagging" safeguard is preserved via the local
> `go.work`/`replace` workflow — we only tag `iot/v0.1.0` after both consumers are green.

## Safety net (already in place)

The behaviour-preservation guard is `cmd/boiler-sensor/wire_contract_test.go`
(`TestWireContractCanonicalSequence`). It pins the **exact ordered MQTT bytes**
for the canonical scenario (both off → CH on → HW on → CH off) plus a heartbeat.
After every refactor step the boiler profile MUST reproduce these bytes verbatim.
Supporting nets now live with the engine in `common/iot`: `logic` and `status` at
100%, the runtime lifecycle tests in `iot/runner_test.go`, and `iot`'s own
wire-contract self-test (boiler-shaped + doorbell-shaped profiles).

Run `go test ./...` after each step. Treat any diff in the golden bytes as a
regression unless the change is deliberate and the golden is updated on purpose.

## What is coupled today (inventory)

| Coupled thing | Location |
|---|---|
| `Input{CH, HW}`, `Event{CHState, HWState}`, `EventCH/HW On/Off` consts | `internal/logic/types.go` |
| `Detector` branches on channel identity (`ch == &d.ch`) | `internal/logic/detector.go:129` |
| `Read() (bool, bool, error)` fixed to 2 channels | `internal/gpio/gpio.go` |
| `Topic = "energy/boiler/sensor/events"` (and system topic) | `internal/mqtt/mqtt.go:12,15` |
| `Payload.Boiler.{ch,hw}` JSON shape | `internal/mqtt/mqtt.go:45-74` |
| `EventCounts{CHOn,CHOff,HWOn,HWOff}` + `CountsJSON` | `internal/logic`, `internal/status/json.go` |
| `Snapshot.CH/HW`, status JSON `ch`/`hw` fields | `internal/status` |
| "Central Heating"/"Hot Water" labels, `msg.boiler` JS parsing | `internal/web/template.go` |

The coupling is **naming and arity, not architecture** — the debounce, baseline,
heartbeat, reconnect/buffer/LWT, HTTP scaffolding are all already generic.

## Target design

### A channel becomes data, not a hardcoded pair

```go
// A logical input the engine tracks. One per GPIO line.
type Channel struct {
    Key      string // stable id, e.g. "ch", "hw", "button"
    Pin      int    // BCM pin
    ActiveLow bool  // raw GPIO inversion (boiler optocoupler = true)
    OnEvent  string // event name emitted on OFF->ON, e.g. "CH_ON", "PRESS"
    OffEvent string // event name emitted on ON->OFF, e.g. "CH_OFF", "RELEASE"
    Debounce time.Duration // per-channel (doorbell press may be shorter than 250ms)
}
```

### A profile is the per-binary domain config

```go
type SensorProfile struct {
    Channels    []Channel
    EventTopic  string // e.g. "energy/boiler/sensor/events"
    SystemTopic string // e.g. "energy/boiler/sensor/system"
    PayloadRoot string // JSON root key: "boiler" / "doorbell"
    WebTitle    string // HTML status page title
}
```

`cmd/boiler-sensor` supplies the CH+HW profile; `cmd/doorbell-sensor` supplies a
one-channel PRESS/RELEASE profile. Everything downstream reads the profile.

### Engine changes

- **`gpio.Reader.Read()` → `([]bool, error)`** (one bool per configured channel,
  inversion applied per `Channel.ActiveLow`). Fake reader takes `[]Sample` of N bits.
- **`logic.Detector`** holds `[]channelState` keyed by `Channel.Key`; `Process`
  takes `Input{Values map[string]bool, Time}` and emits `Event{Key, EventName,
  States map[string]State, Timestamp}`. Drop the `ch == &d.ch` pointer trick.
- **Counts** become `map[string]int` keyed by event name (or per-channel
  on/off), replacing the fixed `EventCounts` struct.
- **`mqtt` payload** built from the profile: root key = `PayloadRoot`, body
  carries event name + a `map[string]ChannelState` of channel states. Topics
  come from the profile, not consts. `SystemEvent`/LWT/heartbeat plumbing is
  unchanged (already generic).
- **`status` + `web`** render channels generically from the snapshot; labels and
  title come from the profile. JSON keys become the channel keys.

> **Boiler back-compat:** the boiler profile (keys `ch`/`hw`, root `boiler`,
> events `CH_ON`…) must serialize to **byte-identical** output. The golden test
> enforces this. If marshalling a `map[string]ChannelState` reorders keys, use an
> ordered encoder or explicit struct for the boiler shape so the wire bytes match.

### Doorbell semantics to get right

A doorbell is a **momentary press**, possibly shorter than the boiler's 250ms
debounce — a symmetric debounce could swallow it. Per-channel `Debounce` plus,
if needed, a rising-edge-with-lockout mode for the press channel. The event of
interest is the PRESS edge, not the release. Expose this as a channel knob; don't
bake a single behaviour into the engine.

## Phased steps (each ends green + golden-clean)

1. **Generalize the engine in place.** ✅ done. Introduced `internal/profile`
   (`Channel`/`SensorProfile`); generic channel-keyed `logic` detector
   (`NewProfileDetector`/`ProcessValues`/`GenericEvent`, pointer trick dropped);
   generic `gpio.ChannelReader` with per-channel `ActiveLow` inversion; ordered
   `mqtt.FormatEventPayload`. Boiler kept byte-identical via a thin 2-channel
   facade over the generic core.
   - *As-built deviation:* `status`/`web` were left boiler-shaped (the facade still
     drives the frozen golden through them). Their N-channel generalization is
     finished in step 2 below, when the doorbell forces it.
2. **Build `common/iot`** (`github.com/sweeney/common/iot`): move the generic
   packages there, **purely generic** (no boiler/CH/HW names — drop the boiler
   facade types). Finish the `status`/`web` N-channel generalization with ordered
   encoders. Add an iot self-test proving a 2-channel boiler-shaped profile
   reproduces the exact legacy boiler bytes, plus a 1-channel smoke test.
3. **Migrate `boiler-sensor` onto `common/iot`** via a `replace` directive; delete
   the extracted `internal/` generic packages; keep `boilerProfile()` in
   `cmd/boiler-sensor`; re-point the wire-contract golden at the generic API
   (expected byte literals UNCHANGED). Suite green, `logic` 100%.
4. **Create `sweeney/doorbell-sensor`** ✅ done — own repo/module: one-channel
   PRESS/RELEASE profile on BCM 17 with a 50ms per-channel debounce, `main`, and
   its own wire-contract golden.
5. **Validate then tag.** ✅ done — all three green, so `iot/v0.1.0` is cut and
   both sensors pin the tag instead of a local `replace`. Local cross-module
   development now uses a `go.work` (gitignored) when `iot` and a sensor are
   edited together. (See `sweeney/common/PLAN.md`.)

## What stays in each sensor repo (does NOT move to `common/iot`)

The `SensorProfile` value: channel/pin/event vocabulary, topics, payload root key,
web title. Everything that says "boiler/CH/HW" or "doorbell/PRESS" lives in the
per-sensor repo (`boiler-sensor`, `doorbell-sensor`), not the shared module.

Also per-repo: the **HTTP/HTML status page** (`internal/web`). The UI is
service-specific presentation, not engine, so each service rolls its own — it
consumes `iot/status` for the snapshot/JSON but owns its handlers and template.

## Post-extraction refinements (done)

Two cleanups after the second consumer validated the API:

- **`iot.Run` runtime.** The ~150 lines of poll-loop + lifecycle glue that were
  duplicated verbatim between the two `main.go`s now live in the `iot` root
  package as `Run(RunConfig)` (signals, STARTUP, baseline→ready+scheduled
  HEARTBEAT, SHUTDOWN, tracker sync, env-based network info). Each binary's
  `main` is now just profile + reader/publisher/tracker + its own web server +
  `iot.Run`. Lifecycle tests live in `iot/runner_test.go`.
- **`iot/web` removed.** The status UI was pulled out of the shared module into
  each service's `internal/web` (see above).

## Open question: should the status page move back into `common/iot`?

The reversal above has not paid off in practice. `boiler-sensor/internal/web` and
`doorbell-sensor/internal/web` are currently **byte-identical** — all four source
files plus the embedded `static/mqtt.min.js` — so the "service-specific
presentation" rationale has produced no divergence, only a duplicated asset and a
second place to fix any bug in the live-update script.

The package is already fully generic: `ViewForProfile` derives everything from
the profile and the template renders N channels. Moving it to `common/iot/web`
would be mechanical, and a sensor that later needs a bespoke page simply stops
calling it. Decide before a third sensor appears.

**Decided 2026-10-25: it stays per repo.** The third sensor, tanktemp, is numeric
and won't use `iot/status`, and the doorbell is parked, so boiler is the only
live copy and there is nothing to deduplicate.

## Deferred / out of scope

- `identity/common` → `sweeney/common`: done separately (2026-10-27/28). `db`,
  `backup` and `web` moved with history; `auth` stays in identity. See
  `common/PLAN.md`.
