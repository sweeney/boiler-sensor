# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go daemon for Raspberry Pi Zero that monitors heating system GPIO inputs and publishes state transitions to MQTT. Runs on 32-bit Raspberry Pi OS Lite but must be fully testable in CI without hardware.

## Build & Test Commands

```bash
go build ./...              # Build all packages
go test ./...               # Run all tests
go vet ./...                # Lint
```

The generic engine (and its 100%-covered logic tests) lives in the
`github.com/sweeney/common/iot` module — run its suite there.

## Architecture

The sensor engine has been extracted into the shared, sensor-agnostic module
`github.com/sweeney/common/iot`. This repo is now just the **boiler binary**: it
supplies a boiler `SensorProfile`, hands off to the shared daemon runtime
`iot.Run` (poll/debounce/publish/lifecycle), and serves its own status page. No
engine code lives here anymore.

```
cmd/boiler-sensor/    # main.go: flags, wiring + boilerProfile(), iot.Run(). The
                      #   ONLY place boiler vocabulary (ch/hw, CH_ON…, pins,
                      #   topics, title) lives. Plus the wire-contract golden test.
internal/web/         # this service's own HTTP status page (presentation, not
                      #   engine). Reads iot/status; each sensor rolls its own UI.
```

The engine packages it consumes: `iot` (the `Run` runtime), `iot/profile`,
`iot/logic`, `iot/gpio`, `iot/mqtt`, `iot/status`. The HTTP/HTML status UI is
deliberately NOT in `common/iot` — it's service-specific presentation, so it
lives here in `internal/web`.

`go.mod` pins the published `github.com/sweeney/common/iot v0.1.0` — there is no
`replace` directive, so the build does not depend on a sibling checkout.

To work on the engine and this binary together, create a **gitignored** `go.work`
(never commit it, and never put one at `~/src/github.com/sweeney/` — a workspace
file there forces every sibling repo into workspace mode and breaks their builds):

```
go 1.21
use (
	/abs/path/to/common/iot
	/abs/path/to/boiler-sensor
)
// Version-specific: a bare replace of a `use`d module is rejected by go.
replace github.com/sweeney/common/iot v0.1.0 => /abs/path/to/common/iot
```

### Engine rules (enforced in `common/iot`, not here)

These invariants are now owned and tested by the `common/iot` module:

- **`iot/logic` zero-deps + 100% coverage**: no imports from GPIO, MQTT, OS, or
  `time.Sleep`; time injected via `time.Time`; 100% test coverage required.
- **Single-place GPIO inversion**: raw GPIO active = logical OFF is applied in
  exactly one place — `iot/gpio`, per each channel's `ActiveLow` flag — never in
  logic. The boiler profile sets `ActiveLow: true` on both channels.

### Boiler profile (the only boiler-specific config)

`boilerProfile()` in `cmd/boiler-sensor/main.go` defines:

- Channels `ch` (Central Heating, BCM 26) and `hw` (Hot Water, BCM 16), both
  `ActiveLow`, emitting `CH_ON`/`CH_OFF` and `HW_ON`/`HW_OFF`.
- Topics `energy/boiler/sensor/events` and `energy/boiler/sensor/system`,
  payload root `boiler`, web title `Boiler Sensor`.

## Runtime Behavior

- Poll interval: `--poll` (default 100ms)
- Debounce duration: `--debounce` (default 250ms)
- Broker: `--broker` (default `tcp://192.168.1.200:1883`)
- HTTP status: `--http` (default `:80`, empty string disables)
- **No MQTT events at startup** - only after baseline established
- Events only on transitions: CH_ON, CH_OFF, HW_ON, HW_OFF

## MQTT Payload Format

Topic: `energy/boiler/sensor/events`

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

## Testing Strategy

Tests must pass on GitHub Actions (Linux amd64) without GPIO or MQTT hardware.

Engine behaviour (debounce, baseline, bounce rejection, back-to-back
transitions) is covered by the `common/iot` suite. This repo keeps
**boiler-specific** coverage:

- `cmd/boiler-sensor/wire_contract_test.go` — the **golden**: pins the EXACT
  ordered MQTT bytes (event payloads + heartbeat) the boiler profile produces
  through the iot engine. Treat any diff in these literals as a regression.
- `cmd/boiler-sensor/lifecycle_golden_test.go` — drives `iot.Run` with the real
  `boilerProfile()` (STARTUP, baseline, ready HEARTBEAT, single and simultaneous
  transitions, scheduled HEARTBEAT with network info, SHUTDOWN) and pins every
  payload, topic and retain flag to what the pre-refactor build published. The
  literals were captured from that build: never edit them to make a test pass.
- `cmd/boiler-sensor/main_test.go` — `boilerProfile()` and `--ws-broker`.
- `internal/web` — status page, `/index.json` and `/healthz`.
- `deploy_test.sh` — `deploy.sh` against stubs (refusals, rollback paths).

## Deploying

`./deploy.sh --check` first (read-only), then `./deploy.sh`. It deploys only a
clean, pushed commit (the build is stamped with the hash and `/healthz` reports
it, so settle commit dates before deploying), verifies `/healthz`, the journal
and the broker heartbeat, and rolls back on its own if any check fails.
`./deploy.sh --rollback` returns to the previous build. Change `deploy.sh` only
with a matching case in `deploy_test.sh`.

## Dependencies

- GPIO: `github.com/warthog618/go-gpiocdev`
- MQTT: Eclipse Paho Go client
- No CGO in test paths
