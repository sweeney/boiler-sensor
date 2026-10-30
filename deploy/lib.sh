# shellcheck shell=bash
# shellcheck disable=SC2034 # the scripts that source this use what it sets
#
# What deploy.sh and setup-pi.sh share. They source it from the repo root
# once they have parsed their arguments (REMOTE, YES). Not run on its own.
# Tests: ./deploy_test.sh, through both scripts and directly.

SERVICE=boiler-sensor
PI_DIR=boiler-sensor # under the remote user's home
LINK=boiler-sensor-arm # ~/boiler-sensor/<LINK> -> the running build
BIN_LINK="${DEPLOY_BIN_LINK:-/usr/local/bin/boiler-sensor}"
UNIT_LINK="${DEPLOY_UNIT_LINK:-/etc/systemd/system/boiler-sensor.service}"
VERIFY_TIMEOUT="${DEPLOY_VERIFY_TIMEOUT:-45}"
REMOTE="${REMOTE:-sweeney@pi0wh}"
HTTP_HOST="${DEPLOY_HTTP_HOST:-${REMOTE#*@}}"

# A stalled transfer hangs rather than errors; ServerAlive turns a dead link
# into an exit code. BatchMode stops ssh waiting for a password nobody types.
SSHOPTS=(-o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o BatchMode=yes)

say() { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '    WARNING: %s\n' "$*" >&2; }
refuse() { printf '\nREFUSED: %s\nNothing on %s was changed.\n' "$*" "$REMOTE" >&2; exit 2; }
# -n: no remote command here reads stdin, and without it ssh would swallow
# what the user types at the confirmation prompt.
r() { ssh -n "${SSHOPTS[@]}" "$REMOTE" "$@"; }

# jget KEY.PATH: read a field from the JSON on stdin; empty if absent.
jget() {
    python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except ValueError:
    d = None
for k in sys.argv[1].split("."):
    d = d.get(k) if isinstance(d, dict) else None
print("" if d is None else str(d).lower() if isinstance(d, bool) else d)' "$1"
}

confirm() {
    [ "$YES" -eq 1 ] && return 0
    local reply
    printf '\n%s [y/N] ' "$1"
    read -r reply || reply=""
    case "$reply" in y | Y | yes) return 0 ;; esac
    refuse "declined"
}

# require_tools: refuse, by name, if a local command the scripts use is missing.
require_tools() {
    local cmd
    for cmd in git go ssh rsync curl python3 shasum; do
        command -v "$cmd" >/dev/null || refuse "$cmd not found"
    done
}

# goarch_for ARCH: set GOARCH and GOARM for a Pi's `uname -m`; fails for
# anything this does not build for.
goarch_for() {
    case "$1" in
        armv6l) GOARCH=arm GOARM=6 ;;
        armv7l) GOARCH=arm GOARM=7 ;;
        aarch64 | arm64) GOARCH=arm64 GOARM="" ;;
        *) return 1 ;;
    esac
}

# commit_dirty: true if uncommitted changes would go into a build.
commit_dirty() {
    ! git diff --quiet HEAD -- || [ -n "$(git ls-files --others --exclude-standard -- cmd internal go.mod go.sum)" ]
}

# commit_pushed: 0 if HEAD is on a remote branch, 1 if not, 2 if the remote
# cannot be fetched. The build names its commit in /healthz, and rewriting a
# commit's date changes its hash: deploying only what is pushed means the
# dates are settled. It fetches first, as a stale origin/main still holds a
# hash force-pushed away.
commit_pushed() {
    git fetch --quiet --all --prune 2>/dev/null || return 2
    [ -n "$(git branch -r --contains HEAD 2>/dev/null)" ]
}

vet_and_test() {
    say "go vet and go test"
    go vet ./... || refuse "go vet failed"
    go test -count=1 ./... >/dev/null || refuse "go test failed (run it to see why)"
    info "pass"
}

# build_for VERSION ARCH: build for ARCH (as goarch_for has set GOARCH and
# GOARM) into LOCAL_BIN, with VERSION stamped in, and refuse a build too small
# to be real.
build_for() {
    local size
    say "building $1"
    mkdir -p bin
    LOCAL_BIN="bin/$SERVICE-$2"
    GOOS=linux GOARCH=$GOARCH GOARM=$GOARM CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=$1" -o "$LOCAL_BIN" ./cmd/$SERVICE
    size=$(wc -c <"$LOCAL_BIN" | tr -d ' ')
    [ "$size" -gt 1000000 ] || refuse "the build is only $size bytes"
    info "$LOCAL_BIN, $size bytes"
}

# upload_build NAME FAIL: put LOCAL_BIN on the Pi as ~/boiler-sensor/NAME,
# through a staging name so a partial upload is never mistaken for a build,
# with retries and a sha256 check. If a step fails, it removes the staged
# file if it can and calls FAIL with the step, which must not return.
upload_build() {
    local name="$1" fail="$2" stage=".incoming-$1" attempt local_sum remote_sum
    say "uploading $name"
    for attempt in 1 2 3; do
        rsync -z --partial --timeout=45 -e "ssh ${SSHOPTS[*]}" "$LOCAL_BIN" "$REMOTE:$PI_DIR/$stage" && break
        warn "upload attempt $attempt failed"
        if [ "$attempt" -eq 3 ]; then
            r "rm -f ~/$PI_DIR/$stage" 2>/dev/null || true
            "$fail" "the upload (three attempts)"
        fi
        sleep 2
    done
    local_sum=$(shasum -a 256 "$LOCAL_BIN" | cut -d' ' -f1)
    remote_sum=$(r "sha256sum ~/$PI_DIR/$stage" | cut -d' ' -f1) || "$fail" "checking its sha256"
    if [ "$local_sum" != "$remote_sum" ]; then
        r "rm -f ~/$PI_DIR/$stage" 2>/dev/null || true
        "$fail" "the sha256 check (the upload arrived corrupted: $remote_sum, expected $local_sum)"
    fi
    r "chmod 0755 ~/$PI_DIR/$stage && mv ~/$PI_DIR/$stage ~/$PI_DIR/$name" || "$fail" "moving it into place"
    info "sha256 verified"
}

http() { curl -sS -f -m 5 "http://$HTTP_HOST$1" 2>/dev/null; }

# version_of NAME: the commit a build file was stamped with, from its name
# (boiler-sensor-YYYYMMDD-HHMMSS-<version>); empty for builds that predate
# version stamping and so have no /healthz.
version_of() {
    local rest="${1#"$SERVICE"-}"
    rest="${rest#????????-??????}"
    printf '%s' "${rest#-}"
}

# wait_healthy VERSION: wait for the running build to answer healthily. With a
# version, /healthz must report that version and status ok. Without one (a
# pre-/healthz build) /index.json must report ready and MQTT connected. The
# last answer is left in LAST_BODY.
wait_healthy() {
    local want="$1" deadline=$((SECONDS + VERIFY_TIMEOUT)) body
    LAST_BODY=""
    while [ $SECONDS -lt $deadline ]; do
        if [ -n "$want" ]; then
            if body=$(http /healthz); then
                LAST_BODY="$body"
                if [ "$(jget version <<<"$body")" = "$want" ] && [ "$(jget status <<<"$body")" = ok ]; then
                    return 0
                fi
            fi
        elif body=$(http /index.json); then
            LAST_BODY="$body"
            if [ "$(jget status.ready <<<"$body")" = true ] && [ "$(jget status.mqtt.connected <<<"$body")" = true ]; then
                return 0
            fi
        fi
        sleep 1
    done
    return 1
}
