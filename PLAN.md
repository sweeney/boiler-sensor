Heating System GPIO → MQTT Daemon (Go)
======================================

Goal
----

Build a small, hardened Go daemon that runs on a **Raspberry Pi Zero (32-bit Raspberry Pi OS Lite)**.\
The program monitors two GPIO inputs representing a heating system, detects debounced state transitions, and publishes structured JSON events to an MQTT broker on the local network.

This project **will be published on GitHub** and **must be fully testable in CI without Raspberry Pi hardware**.

* * * * *

Hardware & Electrical Model
---------------------------

### GPIO Inputs (BCM numbering)

-   **Central Heating (CH)**: BCM **26**

-   **Hot Water (HW)**: BCM **16**

### Signal semantics (inverted logic)

-   Raw GPIO **active** ⇒ logical **OFF**

-   Raw GPIO **inactive** ⇒ logical **ON**

This inversion must be handled explicitly and in exactly one place in the codebase.

* * * * *

Runtime Behavior
----------------

### Sampling & Debounce

-   Poll GPIO inputs at a configurable interval:

    -   Flag: `--poll`

    -   Default: **100ms**

-   Debounce logic:

    -   Flag: `--debounce`

    -   Default: **250ms**

    -   A state change is accepted only if the new logical state remains stable for the full debounce duration.

### Startup Behavior

-   On startup:

    -   Read inputs until both channels reach a debounced stable state.

    -   Use this as the baseline.

    -   **Do NOT publish any MQTT events at startup.**

-   Only publish events for transitions that occur *after* baseline is established.

### Transition Events

Emit events **only on transitions**:

-   `CH_ON`, `CH_OFF`

-   `HW_ON`, `HW_OFF`

No periodic publishing; no repeated events for stable states.

* * * * *

MQTT Requirements
-----------------

### Broker

-   Default broker: `tcp://192.168.1.200:1883`

-   Configurable via flag.

### Device Identity

-   Device ID: **`BOILER_SENSOR`** (fixed)

### Topic

`energy/boiler/sensor/events`

### QoS

-   Use **QoS 0 (at-most-once)** initially.

-   Architecture must allow upgrading QoS or adding retry/queueing later **without changing core logic**.

### Payload Format (JSON)

Each transition publishes **one message**.

Example:

`{
  "boiler": {
    "timestamp": "2026-02-02T22:18:12Z",
    "event": "CH_ON",
    "ch": { "state": "ON" },
    "hw": { "state": "OFF" }
  }
}`

Rules:

-   Top-level key: `"boiler"`

-   Timestamp:

    -   UTC

    -   RFC3339

-   Always include **both** channel states.

-   `"ON"` / `"OFF"` as strings.

* * * * *

Architecture (Strict Separation for Testability)
------------------------------------------------

### Package Layout (Required)

`cmd/boiler-sensor/
internal/logic/
internal/gpio/
internal/mqtt/`

### `internal/logic` (CRITICAL)

-   Pure business logic:

    -   edge detection

    -   debounce

    -   state tracking

-   No imports from:

    -   GPIO

    -   MQTT

    -   OS

    -   time.Sleep

-   Time must be **injectable** (e.g. `time.Time` passed in).

-   Input: logical states (`chOn`, `hwOn`) + current time.

-   Output: zero or more domain events.

✅ **Requirement: 100% test coverage for this package.**\
Every branch, edge case, and debounce path must be tested.

### `internal/gpio`

-   Real implementation:

    -   Use Linux GPIO character device.

    -   Prefer `github.com/warthog618/go-gpiocdev`.

-   Fake implementation:

    -   Used in tests.

    -   Returns scripted raw pin values.

-   Must never leak hardware dependencies into `internal/logic`.

### `internal/mqtt`

-   Real implementation:

    -   Use Eclipse Paho Go client.

-   Fake implementation:

    -   Records published messages for assertions.

-   Publishing failures must not crash the process.

### `cmd/boiler-sensor`

-   Wiring:

    -   flags

    -   logging

    -   main loop

-   No business logic.

-   Logs to stdout/stderr (journald-friendly).

* * * * *

Testing Requirements (Non-Negotiable)
-------------------------------------

### Unit Tests

-   Cover **100%** of `internal/logic`.

-   Include:

    -   stable states → no events

    -   single transitions

    -   bounce/noise shorter than debounce

    -   back-to-back transitions

    -   startup baseline behavior

-   No timing flakiness.

### Integration Tests (Still No Hardware)

-   Use:

    -   fake GPIO

    -   real logic

    -   fake MQTT

-   Feed scripted raw pin samples.

-   Assert:

    -   exact publish order

    -   topic correctness

    -   JSON structure and values

    -   timestamps present and valid

### CI Compatibility

-   Tests must run on:

    -   GitHub Actions

    -   Linux `amd64`

-   No GPIO hardware

-   No MQTT broker

-   `go test ./...` must pass cleanly.

* * * * *

Open Source & CI Requirements
-----------------------------

-   Code will be published on **GitHub**.

-   Include:

    -   `go.mod` with supported Go version

    -   `.github/workflows/ci.yml` running:

        -   checkout

        -   Go setup

        -   `go test ./...`

-   Avoid:

    -   CGO in test paths

    -   system-specific assumptions

-   Keep dependencies minimal and well-maintained.

-   `go vet` must pass cleanly.

systemd Integration
-------------------

Provide a systemd unit file:

-   Starts on boot

-   Restarts on failure

-   Waits for network

Suggested:

-   `After=network-online.target`

-   `Wants=network-online.target`

-   `Restart=on-failure`

-   `RestartSec=2`

Run as non-root **if possible**; otherwise run as root with conservative hardening.

Documentation (README)
----------------------

Must include:

-   What the service does

-   GPIO pin mapping

-   MQTT topic & payload

-   How to run tests locally

-   How CI works without hardware

-   How to install + run on Raspberry Pi

-   Configuration flags

Optional (Nice to Have)
-----------------------

-   `--print-state` flag to read and print raw + logical states once and exit

-   Structured logging

-   Simulation mode using scripted input for manual testing

* * * * *

Success Criteria
----------------

-   Correct behavior is provable via tests.

-   No hardware required to develop or review.

-   CI failures indicate real regressions.

-   The Pi becomes a "dumb sensor," not a debugging platform.