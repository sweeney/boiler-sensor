#!/usr/bin/env bash
#
# Set up a Pi to run boiler-sensor, so that ./deploy.sh can take over.
#
#   ./setup-pi.sh --check        read-only: say what setup would do, change nothing
#   ./setup-pi.sh                set up sweeney@pi0wh (asks before changing anything)
#   ./setup-pi.sh --yes          set up without asking
#   ./setup-pi.sh [opts] user@host   another Pi
#
# Each step runs only if it is still needed, so it is safe to re-run: on a Pi
# that is already set up it changes nothing, and after a failure it carries on
# from where it stopped.
#   1. ~/boiler-sensor, and the unit file in it if there is none (an existing
#      one is left for ./deploy.sh, which installs unit changes with rollback).
#   2. If no build is linked yet, a first one: this commit (clean and pushed,
#      as for deploy.sh), built for the Pi, uploaded as
#      boiler-sensor-<timestamp>-<commit> with a sha256 check, and
#      ~/boiler-sensor/boiler-sensor-arm pointed at it.
#   3. /usr/local/bin/boiler-sensor -> ~/boiler-sensor/boiler-sensor-arm and
#      /etc/systemd/system/boiler-sensor.service -> the unit file. Anything
#      else already at either path is refused, not overwritten.
#   4. The user in the gpio group, systemd reloaded, the service enabled and
#      started, and /index.json reporting ready and MQTT connected.
#
# Exit: 0 set up (or nothing to do, or --check passed); 1 set up, but the
# service did not come up healthy; 2 a check failed or setup was declined,
# nothing changed; 3 a step failed part way: fix it and re-run.
#
# Tests: ./deploy_test.sh (runs everything here against stubs).
set -euo pipefail

SERVICE=boiler-sensor
PI_DIR=boiler-sensor # under the remote user's home
LINK=boiler-sensor-arm # ~/boiler-sensor/<LINK> -> the running build
BIN_LINK="${DEPLOY_BIN_LINK:-/usr/local/bin/boiler-sensor}"
UNIT_LINK="${DEPLOY_UNIT_LINK:-/etc/systemd/system/boiler-sensor.service}"
VERIFY_TIMEOUT="${DEPLOY_VERIFY_TIMEOUT:-45}"

usage() { sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//p;}' "$0"; }

CHECK=0 YES=0 REMOTE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --check) CHECK=1 ;;
        --yes | -y) YES=1 ;;
        -h | --help) usage; exit 0 ;;
        -*) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
        *) [ -z "$REMOTE" ] || { echo "only one host, please" >&2; exit 2; }; REMOTE="$1" ;;
    esac
    shift
done
REMOTE="${REMOTE:-sweeney@pi0wh}"
HTTP_HOST="${DEPLOY_HTTP_HOST:-${REMOTE#*@}}"

cd "$(dirname "${BASH_SOURCE[0]}")"

SSHOPTS=(-o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o BatchMode=yes)

say() { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
refuse() { printf '\nREFUSED: %s\nNothing on %s was changed.\n' "$*" "$REMOTE" >&2; exit 2; }
stopped() { printf '\nFAILED: %s\nSetup stopped part way. Fix that and re-run: it carries on from here.\n' "$*" >&2; exit 3; }
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

# --------------------------------------------------------------------------
# What is there already. Read-only, locally and on the Pi.

for cmd in git go ssh rsync curl python3 shasum; do
    command -v "$cmd" >/dev/null || refuse "$cmd not found"
done

say "checking $REMOTE"
# readlink, or MISSING, or ELSE for something there that is not a symlink.
facts=$(r "uname -m
echo \$HOME
whoami
sudo -n true 2>/dev/null && echo sudo-ok || echo sudo-no
[ -f ~/$PI_DIR/$SERVICE.service ] && echo yes || echo no
readlink ~/$PI_DIR/$LINK || echo MISSING
readlink $BIN_LINK || { [ -e $BIN_LINK ] && echo ELSE || echo MISSING; }
readlink $UNIT_LINK || { [ -e $UNIT_LINK ] && echo ELSE || echo MISSING; }
id -nG | tr ' ' '\n' | grep -qx gpio && echo yes || echo no
e=\$(systemctl is-enabled $SERVICE 2>/dev/null) || true; echo \"\${e:-not-found}\"
systemctl is-active $SERVICE || true") || refuse "cannot ssh to $REMOTE"
{
    read -r remote_arch
    read -r pi_home
    read -r pi_user
    read -r sudo_ok
    read -r have_unit
    read -r build
    read -r bin_link
    read -r unit_link
    read -r in_gpio
    read -r enabled
    read -r active
} <<<"$facts"

case "$remote_arch" in
    armv6l) GOARCH=arm GOARM=6 ;;
    armv7l) GOARCH=arm GOARM=7 ;;
    aarch64 | arm64) GOARCH=arm64 GOARM="" ;;
    *) refuse "$REMOTE is '$remote_arch', which this script does not build for" ;;
esac
[ "$sudo_ok" = sudo-ok ] || refuse "sudo on $REMOTE needs a password; this script cannot type it"

want_bin="$pi_home/$PI_DIR/$LINK"
want_unit="$pi_home/$PI_DIR/$SERVICE.service"
for pair in "$BIN_LINK|$bin_link|$want_bin" "$UNIT_LINK|$unit_link|$want_unit"; do
    IFS='|' read -r path is want <<<"$pair"
    case "$is" in
        MISSING | "$want") ;;
        ELSE) refuse "$path on $REMOTE is not a symlink; move it aside first" ;;
        *) refuse "$path on $REMOTE points at $is, not $want; move it aside first" ;;
    esac
done

todo=()
[ "$have_unit" = yes ] || todo+=("install the unit file as ~/$PI_DIR/$SERVICE.service")
if [ "$build" = MISSING ]; then
    # The first build is named by its commit, as deploy.sh's are.
    VERSION=$(git rev-parse --short=7 HEAD)
    { git diff --quiet HEAD -- && [ -z "$(git ls-files --others --exclude-standard -- cmd internal go.mod go.sum)" ]; } ||
        refuse "uncommitted changes. The version is the commit, so commit first"
    git fetch --quiet --all --prune 2>/dev/null || refuse "cannot fetch from the remote, so cannot tell that $VERSION is pushed"
    [ -n "$(git branch -r --contains HEAD 2>/dev/null)" ] ||
        refuse "$VERSION is not on any remote branch. Settle its date and push it first"
    todo+=("build $VERSION for $remote_arch, upload it and link ~/$PI_DIR/$LINK to it")
fi
[ "$bin_link" = "$want_bin" ] || todo+=("link $BIN_LINK -> $want_bin")
[ "$unit_link" = "$want_unit" ] || todo+=("link $UNIT_LINK -> $want_unit")
[ "$in_gpio" = yes ] || todo+=("add $pi_user to the gpio group")
[ "$enabled" = enabled ] || todo+=("enable $SERVICE")
start=0
if [ "$active" != active ] || [ "$build" = MISSING ]; then
    start=1
    todo+=("start $SERVICE and wait for it to report ready and MQTT connected")
fi

if [ ${#todo[@]} -eq 0 ]; then
    say "$REMOTE is already set up: nothing to do (./deploy.sh from here)"
    exit 0
fi

echo
say "$([ "$CHECK" -eq 1 ] && echo "check passed. Setup would" || echo "setup will"):"
for t in "${todo[@]}"; do info "$t"; done
[ "$CHECK" -eq 0 ] || exit 0

if [ "$build" = MISSING ]; then
    say "go vet and go test"
    go vet ./... || refuse "go vet failed"
    go test -count=1 ./... >/dev/null || refuse "go test failed (run it to see why)"
    say "building $VERSION"
    mkdir -p bin
    LOCAL_BIN="bin/$SERVICE-$remote_arch"
    GOOS=linux GOARCH=$GOARCH GOARM=$GOARM CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=$VERSION" -o "$LOCAL_BIN" ./cmd/$SERVICE
fi
confirm "Set up $SERVICE on $REMOTE as above?"

# --------------------------------------------------------------------------
# Each step only if needed. From here a failure stops with exit 3.

r "mkdir -p ~/$PI_DIR" || stopped "cannot create ~/$PI_DIR"
if [ "$have_unit" != yes ]; then
    say "installing the unit file"
    rsync -e "ssh ${SSHOPTS[*]}" "$SERVICE.service" "$REMOTE:$PI_DIR/$SERVICE.service" || stopped "uploading the unit file failed"
fi

if [ "$build" = MISSING ]; then
    NEW="$SERVICE-$(date +%Y%m%d-%H%M%S)-$VERSION"
    STAGE=".incoming-$NEW"
    say "uploading $NEW"
    rsync -z --partial --timeout=45 -e "ssh ${SSHOPTS[*]}" "$LOCAL_BIN" "$REMOTE:$PI_DIR/$STAGE" || stopped "the upload failed"
    local_sum=$(shasum -a 256 "$LOCAL_BIN" | cut -d' ' -f1)
    remote_sum=$(r "sha256sum ~/$PI_DIR/$STAGE" | cut -d' ' -f1) || stopped "cannot check the upload's sha256"
    [ "$local_sum" = "$remote_sum" ] || stopped "upload corrupted: sha256 $remote_sum, expected $local_sum"
    r "cd ~/$PI_DIR && chmod 0755 $STAGE && mv $STAGE $NEW && ln -sfn $NEW $LINK" || stopped "cannot link $NEW"
    info "sha256 verified, ~/$PI_DIR/$LINK -> $NEW"
fi

reload=0
if [ "$bin_link" != "$want_bin" ]; then
    say "linking $BIN_LINK"
    r "sudo ln -s '$want_bin' '$BIN_LINK'" || stopped "cannot link $BIN_LINK"
fi
if [ "$unit_link" != "$want_unit" ]; then
    say "linking $UNIT_LINK"
    r "sudo ln -s '$want_unit' '$UNIT_LINK'" || stopped "cannot link $UNIT_LINK"
    reload=1
fi
if [ "$in_gpio" != yes ]; then
    # The service gets gpio from its unit (SupplementaryGroups); this is for
    # running boiler-sensor --print-state by hand, after logging in again.
    say "adding $pi_user to the gpio group"
    r "sudo usermod -aG gpio $pi_user" || stopped "cannot add $pi_user to gpio"
fi
if [ $reload -eq 1 ] || [ "$have_unit" != yes ]; then
    r "sudo systemctl daemon-reload" || stopped "systemctl daemon-reload failed"
fi
if [ "$enabled" != enabled ]; then
    say "enabling $SERVICE"
    r "sudo systemctl enable $SERVICE" || stopped "cannot enable $SERVICE"
fi
[ $start -eq 1 ] || { say "$REMOTE is set up"; exit 0; }

say "starting $SERVICE"
r "sudo systemctl restart $SERVICE" || stopped "$SERVICE did not start: sudo journalctl -u $SERVICE on $REMOTE says why"
deadline=$((SECONDS + VERIFY_TIMEOUT)) body=""
while [ $SECONDS -lt $deadline ]; do
    if body=$(curl -sS -f -m 5 "http://$HTTP_HOST/index.json" 2>/dev/null) &&
        [ "$(jget status.ready <<<"$body")" = true ] && [ "$(jget status.mqtt.connected <<<"$body")" = true ]; then
        say "$SERVICE is running on $REMOTE: ch=$(jget status.ch <<<"$body") hw=$(jget status.hw <<<"$body"). ./deploy.sh from here"
        exit 0
    fi
    sleep 1
done
echo >&2
echo "!! $SERVICE is set up on $REMOTE but did not come up healthy within ${VERIFY_TIMEOUT}s" >&2
echo "!! Last answer: ${body:-none}" >&2
r "sudo journalctl -u $SERVICE -n 30 --no-pager -o cat" >&2 || true
exit 1
