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
#      else already at these paths, or at ~/boiler-sensor/boiler-sensor-arm, is
#      refused, not overwritten.
#   4. The user in the gpio group, systemd reloaded, the service enabled and
#      started, and the build answering healthily.
#
# Exit: 0 set up (or nothing to do, or --check passed); 1 set up, but the
# service did not come up healthy; 2 a check failed or setup was declined,
# nothing changed; 3 a step failed part way: fix it and re-run.
#
# Tests: ./deploy_test.sh (runs everything here against stubs).
set -euo pipefail

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

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source-path=SCRIPTDIR source=deploy/lib.sh
source deploy/lib.sh

stopped() { printf '\nFAILED: %s\nSetup stopped part way. Fix that and re-run: it carries on from here.\n' "$*" >&2; exit 3; }
# shellcheck disable=SC2317,SC2329 # upload_build calls it by name
upload_failed() { stopped "$1 failed"; }

# --------------------------------------------------------------------------
# What is there already. Read-only, locally and on the Pi.

require_tools

say "checking $REMOTE"
# readlink, or MISSING, or ELSE for something there that is not a symlink.
link_or() { echo "readlink $1 || { [ -e $1 ] && echo ELSE || echo MISSING; }"; }
facts=$(r "uname -m
echo \$HOME
whoami
sudo -n true 2>/dev/null && echo sudo-ok || echo sudo-no
[ -f ~/$PI_DIR/$SERVICE.service ] && echo yes || echo no
$(link_or "\$HOME/$PI_DIR/$LINK")
$(link_or "$BIN_LINK")
$(link_or "$UNIT_LINK")
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

goarch_for "$remote_arch" || refuse "$REMOTE is '$remote_arch', which this script does not build for"
[ "$sudo_ok" = sudo-ok ] || refuse "sudo on $REMOTE needs a password; this script cannot type it"
[ "$build" != ELSE ] || refuse "\$HOME/$PI_DIR/$LINK on $REMOTE is not a symlink (an older layout?); move it aside first"

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
    ! commit_dirty || refuse "uncommitted changes. The version is the commit, so commit first"
    pushed=0
    commit_pushed || pushed=$?
    [ $pushed -ne 2 ] || refuse "cannot reach the remote, so cannot tell that $VERSION is pushed"
    [ $pushed -ne 3 ] || refuse "the remote has commits this checkout lacks (git fetch, then again), so cannot tell that $VERSION is pushed"
    [ $pushed -eq 0 ] || refuse "$VERSION is not on any remote branch. Settle its date and push it first"
    todo+=("build $VERSION for $remote_arch, upload it and link ~/$PI_DIR/$LINK to it")
fi
[ "$bin_link" = "$want_bin" ] || todo+=("link $BIN_LINK -> $want_bin")
[ "$unit_link" = "$want_unit" ] || todo+=("link $UNIT_LINK -> $want_unit")
[ "$in_gpio" = yes ] || todo+=("add $pi_user to the gpio group")
[ "$enabled" = enabled ] || todo+=("enable $SERVICE")
start=0
if [ "$active" != active ] || [ "$build" = MISSING ]; then
    start=1
    todo+=("start $SERVICE and wait for it to answer healthily")
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
    vet_and_test
    build_for "$VERSION" "$remote_arch"
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
    build="$SERVICE-$(date +%Y%m%d-%H%M%S)-$VERSION"
    upload_build "$build" upload_failed
    r "cd ~/$PI_DIR && ln -sn $build $LINK" || stopped "cannot link ~/$PI_DIR/$LINK to $build"
    info "linked ~/$PI_DIR/$LINK -> $build"
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
if wait_healthy "$(version_of "$build")"; then
    say "$SERVICE is running on $REMOTE. ./deploy.sh from here"
    exit 0
fi
echo >&2
echo "!! $SERVICE is set up on $REMOTE but did not come up healthy within ${VERIFY_TIMEOUT}s" >&2
echo "!! Last answer: ${LAST_BODY:-none}" >&2
r "sudo journalctl -u $SERVICE -n 30 --no-pager -o cat" >&2 || true
exit 1
