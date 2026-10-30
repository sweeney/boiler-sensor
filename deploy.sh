#!/usr/bin/env bash
#
# Build, deploy and verify boiler-sensor on its Pi, rolling back on its own if
# the new build does not come up healthy.
#
#   ./deploy.sh --check          read-only: run every pre-flight check, change nothing
#   ./deploy.sh                  deploy to sweeney@pi0wh (asks before changing anything)
#   ./deploy.sh --yes            deploy without asking
#   ./deploy.sh --rollback       switch back to the build that ran before the last deploy
#   ./deploy.sh --check --rollback   read-only: what --rollback would do
#   ./deploy.sh [opts] user@host another Pi
#
# What a deploy does:
#   1. Pre-flight, all read-only (apart from a `git fetch`): a clean, pushed
#      commit (the version is the commit hash, so it must exist on GitHub, as
#      fetched now, and its date must be final),
#      `go vet` and `go test`, the Pi reachable with passwordless sudo, the
#      service and its symlinks as expected, the current build healthy, and the
#      broker's retained heartbeat readable from here.
#   2. Build for the Pi's architecture, stamped with the commit.
#   3. Upload under a new name, check the sha256, and only then switch the
#      symlink and restart. The old build stays on disk, and its name is kept
#      in ~/boiler-sensor/.previous for --rollback.
#   4. Verify: /healthz answers with this version and status "ok" (baselined,
#      MQTT connected), stays up, the journal shows no fatal error, and the
#      broker holds a HEARTBEAT from this run (what statehouse sees).
#   5. If any of 4 fails, switch back to the previous build, restart it and
#      check it is healthy again.
#
# A restart costs a few seconds without monitoring. The new process publishes
# its state as a retained HEARTBEAT as soon as it has a baseline, so a
# transition missed during the restart is corrected within a second of it.
#
# Exit: 0 deployed and verified (or --check passed); 1 the new build failed
# verification and was rolled back; 2 a pre-flight check failed or the deploy
# was declined, nothing changed; 3 rollback did not come up healthy either:
# needs a person.
#
# Tests: ./deploy_test.sh (runs everything here against stubs).
set -euo pipefail

SERVICE=boiler-sensor
PI_DIR=boiler-sensor # under the remote user's home
LINK=boiler-sensor-arm # ~/boiler-sensor/<LINK> -> the running build
BIN_LINK="${DEPLOY_BIN_LINK:-/usr/local/bin/boiler-sensor}"
SYSTEM_TOPIC=energy/boiler/sensor/system
KEEP=3
VERIFY_TIMEOUT="${DEPLOY_VERIFY_TIMEOUT:-45}"
SETTLE="${DEPLOY_SETTLE:-5}"

usage() { sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//p;}' "$0"; }

MODE=deploy CHECK=0 YES=0 ALLOW_DIRTY=0 REMOTE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --check) CHECK=1 ;;
        --rollback) MODE=rollback ;;
        --yes | -y) YES=1 ;;
        --allow-dirty) ALLOW_DIRTY=1 ;;
        -h | --help) usage; exit 0 ;;
        -*) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
        *) [ -z "$REMOTE" ] || { echo "only one host, please" >&2; exit 2; }; REMOTE="$1" ;;
    esac
    shift
done
REMOTE="${REMOTE:-sweeney@pi0wh}"
HTTP_HOST="${DEPLOY_HTTP_HOST:-${REMOTE#*@}}"

cd "$(dirname "${BASH_SOURCE[0]}")"

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
# pre-/healthz build) /index.json must report ready and MQTT connected.
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

# retained_heartbeat: the broker's retained system message, or nothing.
retained_heartbeat() {
    [ -n "${BROKER_HOST:-}" ] || return 1
    mosquitto_sub -h "$BROKER_HOST" -p "$BROKER_PORT" -t "$SYSTEM_TOPIC" -C 1 -W 5 2>/dev/null
}

# switch_to NAME: point the service at build NAME and restart it.
switch_to() {
    r "cd ~/$PI_DIR && ln -sfn '$1' $LINK && sudo systemctl restart $SERVICE"
}

rollback_to() {
    local prev="$1"
    say "rolling back to $prev"
    switch_to "$prev"
    if wait_healthy "$(version_of "$prev")"; then
        info "$prev is running and healthy again"
        return 0
    fi
    echo >&2
    echo "!! ROLLBACK DID NOT COME UP HEALTHY. $SERVICE needs a person now." >&2
    echo "!! Last answer: ${LAST_BODY:-none}" >&2
    r "systemctl status $SERVICE --no-pager -n 0; journalctl -u $SERVICE -n 30 --no-pager -o cat" >&2 || true
    exit 3
}

confirm() {
    [ "$YES" -eq 1 ] && return 0
    local reply
    printf '\n%s [y/N] ' "$1"
    read -r reply || reply=""
    case "$reply" in y | Y | yes) return 0 ;; esac
    refuse "declined"
}

# --------------------------------------------------------------------------
# Pre-flight. Everything here is read-only, locally and on the Pi.

for cmd in git go ssh rsync curl python3 shasum; do
    command -v "$cmd" >/dev/null || refuse "$cmd not found"
done

say "checking $REMOTE"
remote_facts=$(r "uname -m
readlink ~/$PI_DIR/$LINK || echo MISSING
readlink $BIN_LINK || echo MISSING
systemctl is-active $SERVICE || true
sudo -n true 2>/dev/null && echo sudo-ok || echo sudo-no
df -Pk ~ | awk 'NR==2 {print \$4}'
cat ~/$PI_DIR/.previous 2>/dev/null || echo NONE
ls -1t ~/$PI_DIR | grep '^$SERVICE-2' || true") || refuse "cannot ssh to $REMOTE"

{
    read -r remote_arch
    read -r CURRENT
    read -r bin_link
    read -r active
    read -r sudo_ok
    read -r free_kb
    read -r recorded_prev
    builds=$(cat)
} <<<"$remote_facts"

case "$remote_arch" in
    armv6l) GOARCH=arm GOARM=6 ;;
    armv7l) GOARCH=arm GOARM=7 ;;
    aarch64 | arm64) GOARCH=arm64 GOARM="" ;;
    *) refuse "$REMOTE is '$remote_arch', which this script does not build for" ;;
esac
info "arch $remote_arch, service $active"
[ "$sudo_ok" = sudo-ok ] || refuse "sudo on $REMOTE needs a password; this script cannot type it"
[ "$CURRENT" != MISSING ] || refuse "\$HOME/$PI_DIR/$LINK is missing: this host was never set up (see README)"
[ "$bin_link" = "${bin_link%"/$PI_DIR/$LINK"}/$PI_DIR/$LINK" ] ||
    refuse "$BIN_LINK points at '$bin_link', not ~/$PI_DIR/$LINK; switching that link would not change what runs"
[ "${free_kb:-0}" -gt 51200 ] || refuse "less than 50MB free in the home directory on $REMOTE"
info "running build: $CURRENT"

# unhealthy WHY: a deploy measures the new build against the old one, and its
# rollback expects the old one back as healthy as it was. If it isn't healthy
# now (the broker down, say), the deploy can only fail, and its rollback be
# judged failed too. A rollback goes ahead: this may be why it is wanted.
unhealthy() {
    [ "$MODE" = rollback ] && { warn "$1"; return 0; }
    refuse "$1. Deploy once it is"
}

# The state now, to compare with after.
pre_body=$(http /index.json) || pre_body=""
if [ -n "$pre_body" ]; then
    PRE_CH=$(jget status.ch <<<"$pre_body")
    PRE_HW=$(jget status.hw <<<"$pre_body")
    pre_ready=$(jget status.ready <<<"$pre_body")
    pre_mqtt=$(jget status.mqtt.connected <<<"$pre_body")
    broker=$(jget status.mqtt.broker <<<"$pre_body")
    info "state: ch=$PRE_CH hw=$PRE_HW ready=$pre_ready mqtt=$pre_mqtt, up $(jget status.uptime_seconds <<<"$pre_body")s"
    [ "$pre_ready" = true ] && [ "$pre_mqtt" = true ] ||
        unhealthy "the running build is not healthy right now (ready=$pre_ready mqtt=$pre_mqtt)"
    # tcp://host:port -> host, port
    broker="${broker#*://}"
    BROKER_HOST="${broker%%:*}"
    BROKER_PORT="${broker##*:}"
    [ "$BROKER_PORT" != "$BROKER_HOST" ] || BROKER_PORT=1883
else
    unhealthy "the running build is not healthy right now: http://$HTTP_HOST/index.json did not answer"
fi

if [ "$MODE" = rollback ]; then
    [ "$recorded_prev" != NONE ] || refuse "no ~/$PI_DIR/.previous: nothing recorded to roll back to"
    grep -qx "$recorded_prev" <<<"$builds" || refuse "the recorded previous build $recorded_prev is no longer on $REMOTE"
    [ "$recorded_prev" != "$CURRENT" ] || refuse "$recorded_prev is already running"
    if [ "$CHECK" -eq 1 ]; then
        echo
        say "check passed. A rollback would switch $REMOTE from $CURRENT back to $recorded_prev and restart $SERVICE"
        exit 0
    fi
    confirm "Switch $REMOTE from $CURRENT back to $recorded_prev and restart $SERVICE?"
    r "printf '%s\n' '$CURRENT' > ~/$PI_DIR/.previous"
    rollback_to "$recorded_prev"
    say "$SERVICE is back on $recorded_prev (./deploy.sh --rollback again to undo)"
    exit 0
fi

# Can the result be checked end to end, the way statehouse sees it?
BROKER_CHECK=0
if command -v mosquitto_sub >/dev/null && [ -n "${BROKER_HOST:-}" ]; then
    if hb=$(retained_heartbeat) && [ -n "$hb" ]; then
        BROKER_CHECK=1
        info "broker $BROKER_HOST:$BROKER_PORT: retained $(jget status.event <<<"$hb") from a run started $(jget status.start_time <<<"$hb")"
    else
        warn "cannot read the retained heartbeat from $BROKER_HOST:$BROKER_PORT; the broker check will be skipped"
    fi
else
    warn "no mosquitto_sub or no broker address; the broker check will be skipped"
fi

say "checking the commit"
VERSION=$(git rev-parse --short=7 HEAD)
branch=$(git rev-parse --abbrev-ref HEAD)
info "$branch at $VERSION: $(git log -1 --format='%s (%ad)' --date=iso)"
if ! git diff --quiet HEAD -- || [ -n "$(git ls-files --others --exclude-standard -- cmd internal go.mod go.sum)" ]; then
    if [ "$ALLOW_DIRTY" -eq 1 ]; then
        VERSION="$VERSION-dirty"
        warn "uncommitted changes: deploying as $VERSION"
    elif [ "$CHECK" -eq 1 ]; then
        warn "uncommitted changes: a deploy would refuse (commit first, or --allow-dirty)"
    else
        refuse "uncommitted changes. The version is the commit, so commit first (or --allow-dirty)"
    fi
fi
# Rewriting a commit's date changes its hash, and the build names its hash in
# /healthz. Deploying only what is on GitHub means the dates are settled.
# Fetch first: a stale origin/main still holds a hash force-pushed away.
if ! git fetch --quiet --all --prune 2>/dev/null; then
    if [ "$CHECK" -eq 1 ]; then
        warn "cannot fetch from the remote to see that $VERSION is pushed: a deploy would refuse"
    else
        refuse "cannot fetch from the remote, so cannot tell that $VERSION is still pushed"
    fi
elif [ -z "$(git branch -r --contains HEAD 2>/dev/null)" ]; then
    if [ "$CHECK" -eq 1 ]; then
        warn "$VERSION is not pushed: a deploy would refuse"
    else
        refuse "$VERSION is not on any remote branch. Settle its date and push it first"
    fi
fi

say "go vet and go test"
go vet ./... || refuse "go vet failed"
go test -count=1 ./... >/dev/null || refuse "go test failed (run it to see why)"
info "pass"

if [ "$CHECK" -eq 1 ]; then
    echo
    say "check passed. A deploy would:"
    info "build $VERSION for $remote_arch and upload it as $SERVICE-<timestamp>-$VERSION"
    info "switch ~/$PI_DIR/$LINK from $CURRENT and restart $SERVICE"
    info "verify /healthz, the journal$([ $BROKER_CHECK -eq 1 ] && echo ' and the broker heartbeat'), or roll back"
    exit 0
fi

# --------------------------------------------------------------------------
# Build and upload. Nothing that runs changes until the confirmation.

say "building $VERSION"
mkdir -p bin
LOCAL_BIN="bin/$SERVICE-$remote_arch"
GOOS=linux GOARCH=$GOARCH GOARM=$GOARM CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$LOCAL_BIN" ./cmd/$SERVICE
size=$(wc -c <"$LOCAL_BIN" | tr -d ' ')
[ "$size" -gt 1000000 ] || refuse "the build is only $size bytes"
info "$LOCAL_BIN, $size bytes"

NEW="$SERVICE-$(date +%Y%m%d-%H%M%S)-$VERSION"
confirm "Upload $NEW to $REMOTE, switch $SERVICE to it from $CURRENT and restart?"

say "uploading $NEW"
STAGE=".incoming-$NEW"
cleanup_stage() { r "rm -f ~/$PI_DIR/$STAGE" 2>/dev/null || true; }
uploaded=0
for attempt in 1 2 3; do
    if rsync -z --partial --timeout=45 -e "ssh ${SSHOPTS[*]}" "$LOCAL_BIN" "$REMOTE:$PI_DIR/$STAGE"; then
        uploaded=1
        break
    fi
    warn "upload attempt $attempt failed"
    sleep 2
done
[ $uploaded -eq 1 ] || { cleanup_stage; refuse "upload failed three times"; }
local_sum=$(shasum -a 256 "$LOCAL_BIN" | cut -d' ' -f1)
remote_sum=$(r "sha256sum ~/$PI_DIR/$STAGE" | cut -d' ' -f1)
[ "$local_sum" = "$remote_sum" ] || { cleanup_stage; refuse "upload corrupted: sha256 $remote_sum, expected $local_sum"; }
r "chmod 0755 ~/$PI_DIR/$STAGE && mv ~/$PI_DIR/$STAGE ~/$PI_DIR/$NEW"
info "sha256 verified"

# The unit file is a symlink into ~/boiler-sensor; refresh it only if changed.
if ! r "cat ~/$PI_DIR/$SERVICE.service" 2>/dev/null | cmp -s - "$SERVICE.service"; then
    say "the unit file changed: installing it"
    rsync -e "ssh ${SSHOPTS[*]}" "$SERVICE.service" "$REMOTE:$PI_DIR/$SERVICE.service"
    r "sudo systemctl daemon-reload"
fi

# --------------------------------------------------------------------------
# Switch, restart, verify.

say "switching to $NEW and restarting"
r "printf '%s\n' '$CURRENT' > ~/$PI_DIR/.previous"
since=$(r "date +%s")
switch_to "$NEW"

failed=""
if ! wait_healthy "$VERSION"; then
    failed="/healthz did not report $VERSION and status ok within ${VERIFY_TIMEOUT}s (last: ${LAST_BODY:-no answer})"
else
    info "/healthz: $VERSION ok"
    start_time=$(jget start_time <<<"$LAST_BODY")
    # Still the same process a few seconds later? A crash loop restarts under
    # systemd and would show a new start_time.
    sleep "$SETTLE"
    if ! later=$(http /healthz) || [ "$(jget start_time <<<"$later")" != "$start_time" ] ||
        [ "$(jget status <<<"$later")" != ok ]; then
        failed="it did not stay up: ${later:-no answer}"
    fi
fi

if [ -z "$failed" ]; then
    journal=$(r "journalctl -u $SERVICE --since @$since --no-pager -o cat" || true)
    if grep -qiE 'fatal|panic' <<<"$journal"; then
        failed="the journal shows: $(grep -iE 'fatal|panic' <<<"$journal" | head -3)"
    fi
fi

if [ -z "$failed" ] && [ $BROKER_CHECK -eq 1 ]; then
    hb=$(retained_heartbeat) || hb=""
    if [ "$(jget status.event <<<"$hb")" != HEARTBEAT ] || [ "$(jget status.start_time <<<"$hb")" != "$start_time" ]; then
        failed="the broker's retained message is not a HEARTBEAT from this run: ${hb:-nothing}"
    else
        info "broker holds a HEARTBEAT from this run"
    fi
fi

if [ -n "$failed" ]; then
    echo >&2
    echo "!! $NEW FAILED VERIFICATION: $failed" >&2
    r "journalctl -u $SERVICE --since @$since --no-pager -o cat" 2>/dev/null | tail -20 | sed 's/^/   | /' >&2 || true
    r "printf '%s\n' '$NEW' > ~/$PI_DIR/.previous"
    rollback_to "$CURRENT"
    echo "$NEW stays on $REMOTE for a look; the next deploy's cleanup removes it." >&2
    exit 1
fi

now_ch=$(jget channels.ch <<<"$LAST_BODY")
now_hw=$(jget channels.hw <<<"$LAST_BODY")
if [ -n "${PRE_CH:-}" ] && { [ "$now_ch" != "$PRE_CH" ] || [ "$now_hw" != "$PRE_HW" ]; }; then
    warn "state changed across the deploy: ch $PRE_CH->$now_ch hw $PRE_HW->$now_hw (a real transition, or a reading to look at)"
else
    info "state unchanged: ch=$now_ch hw=$now_hw"
fi

# Keep the newest $KEEP builds, and never the one just switched away from.
r "cd ~/$PI_DIR && ls -1t | grep '^$SERVICE-2' | tail -n +$((KEEP + 1)) | grep -vx -e '$NEW' -e '$CURRENT' | xargs -r rm -f --"

echo
say "$SERVICE $VERSION is running on $REMOTE"
r "journalctl -u $SERVICE --since @$since --no-pager -o cat" | tail -8 | sed 's/^/    /' || true
info "to undo: ./deploy.sh --rollback (back to $CURRENT)"
