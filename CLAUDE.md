# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go daemon for Raspberry Pi Zero that monitors heating system GPIO inputs and publishes state transitions to MQTT. Runs on 32-bit Raspberry Pi OS Lite but must be fully testable in CI without hardware.

## Build & Test Commands

```bash
go build ./...              # Build all packages
go test ./...               # Run all tests
go test -cover ./internal/logic/  # Test with coverage (100% required for logic)
go vet ./...                # Lint
```

## Architecture

**Strict separation is critical for testability:**

```
cmd/boiler-sensor/    # Main entry point, flags, wiring. NO business logic.
internal/logic/       # Pure business logic. NO external dependencies.
internal/gpio/        # GPIO abstraction (real + fake implementations)
internal/mqtt/        # MQTT abstraction (real + fake implementations)
internal/status/      # Thread-safe status tracker (shared by web + future LED drivers)
internal/web/         # HTTP status server (JSON + HTML endpoints)
```

### `internal/logic` Rules (Non-Negotiable)

- **Zero imports** from GPIO, MQTT, OS, or `time.Sleep`
- Time must be injectable via `time.Time` parameter
- Input: logical states (chOn, hwOn) + current time
- Output: domain events
- **100% test coverage required**

### GPIO Signal Inversion

Raw GPIO active = logical OFF. This inversion must be handled in exactly one place (in `internal/gpio`, not in logic).

- Central Heating: BCM 26
- Hot Water: BCM 16

## Runtime Behavior

- Poll interval: `--poll` (default 100ms)
- Debounce duration: `--debounce` (default 250ms)
- Broker: `--broker` (default `tcp://192.168.1.200:1883`)
- HTTP status: `--http` (default `:80`, empty string disables)
- **No MQTT events at startup** - only after baseline established
- Events only on transitions: CH_ON, CH_OFF, HW_ON, HW_OFF

## MQTT Payload Format

Topic: `energy/BOILER_SENSOR/SENSOR/heating`

```json
{
  "heating": {
    "timestamp": "2026-02-02T22:18:12Z",
    "event": "CH_ON",
    "ch": { "state": "ON" },
    "hw": { "state": "OFF" }
  }
}
```

## Testing Strategy

Tests must pass on GitHub Actions (Linux amd64) without GPIO or MQTT hardware.

**Unit tests for `internal/logic`** must cover:
- Stable states producing no events
- Single transitions
- Bounce/noise shorter than debounce duration
- Back-to-back transitions
- Startup baseline behavior

**Integration tests** use fake GPIO + fake MQTT to verify exact publish order, topics, JSON structure, and timestamps.

## Dependencies

- GPIO: `github.com/warthog618/go-gpiocdev`
- MQTT: Eclipse Paho Go client
- No CGO in test paths
