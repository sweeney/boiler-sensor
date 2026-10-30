#!/usr/bin/env bash
#
# Build, deploy and verify boiler-sensor on its Pi, rolling back on its own if
# the new build does not come up healthy.
#
#   ./deploy.sh --check          read-only: run every pre-flight check, change nothing
#   ./deploy.sh                  deploy to sweeney@pi0wh (asks before changing anything)
#   ./deploy.sh --yes            deploy without asking
#   ./deploy.sh --allow-unhealthy  deploy a fix over a running build that is not
#                                healthy (refused otherwise); its rollback then
#                                accepts the old build back as it was
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
#      in ~/boiler-sensor/.previous for --rollback. Each build's unit file is
#      kept with it, in ~/boiler-sensor/.units/<build>.service, and a switch to
#      a build (forward or back) installs its unit first if it differs.
#   4. Verify: /healthz answers with this version and status "ok" (baselined,
#      MQTT connected), stays up, the journal shows no fatal error, and the
#      broker holds a HEARTBEAT from this run (what statehouse sees).
#   5. If any of 4 fails, switch back to the previous build and its unit file,
#      restart it and check it is healthy again. If installing the new unit
#      file fails, the old one is put back and nothing is restarted (exit 2).
#
# A restart costs a few seconds without monitoring. The new process publishes
# its state as a retained HEARTBEAT as soon as it has a baseline, so a
# transition missed during the restart is corrected within a second of it.
#
# --check goes through every check and exits 2 if a deploy would refuse.
#
# Exit: 0 deployed and verified (or --check passed); 1 the new build failed
# to start or to verify and was rolled back; 2 a pre-flight check failed, the
# link to the Pi dropped before the switch, or the deploy was declined:
# nothing that runs changed; 3 the rollback did not come up healthy (or could
# not be started): needs a person.
#
# Tests: ./deploy_test.sh (runs everything here against stubs).
set -euo pipefail

SYSTEM_TOPIC=energy/boiler/sensor/system
KEEP=3
SETTLE="${DEPLOY_SETTLE:-5}"

usage() { sed -n '3,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//p;}' "$0"; }

MODE=deploy CHECK=0 YES=0 ALLOW_DIRTY=0 ALLOW_UNHEALTHY=0 REMOTE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --check) CHECK=1 ;;
        --rollback) MODE=rollback ;;
        --yes | -y) YES=1 ;;
        --allow-dirty) ALLOW_DIRTY=1 ;;
        --allow-unhealthy) ALLOW_UNHEALTHY=1 ;;
        -h | --help) usage; exit 0 ;;
        -*) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
        *) [ -z "$REMOTE" ] || { echo "only one host, please" >&2; exit 2; }; REMOTE="$1" ;;
    esac
    shift
done

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source-path=SCRIPTDIR source=deploy/lib.sh
source deploy/lib.sh

# would_refuse WHY: under --check, note what a deploy would refuse for and
# carry on checking; --check fails at the end.
PROBLEMS=()
would_refuse() { warn "$1: a deploy would refuse"; PROBLEMS+=("$1"); }
# retained_heartbeat: the broker's retained system message, or nothing.
retained_heartbeat() {
    [ -n "${BROKER_HOST:-}" ] || return 1
    mosquitto_sub -h "$BROKER_HOST" -p "$BROKER_PORT" -t "$SYSTEM_TOPIC" -C 1 -W 5 2>/dev/null
}

# switch_to NAME: point the service at build NAME and restart it.
switch_to() {
    r "cd ~/$PI_DIR && ln -sfn '$1' $LINK && sudo systemctl restart $SERVICE"
}

# Each build's unit file, kept beside it so that switching to a build, forward
# or back, can put its unit in place too. Builds from before this have none;
# switching to one leaves the unit file as it is.
UNITS=.units

# install_unit NAME: make NAME's unit file the installed one, if it has one
# that differs, and have systemd reload it.
install_unit() {
    r "cd ~/$PI_DIR && if [ -f $UNITS/$1.service ] && ! cmp -s $UNITS/$1.service $SERVICE.service; then
        cp $UNITS/$1.service .$SERVICE.service.tmp && mv .$SERVICE.service.tmp $SERVICE.service &&
        sudo systemctl daemon-reload && echo '    installed the unit file of $1'
    fi"
}

# needs_person WHAT: stop, with what is known, for someone to look at the Pi.
needs_person() {
    echo >&2
    echo "!! $1. $SERVICE needs a person now." >&2
    echo "!! Last answer: ${LAST_BODY:-none}" >&2
    r "systemctl status $SERVICE --no-pager -n 0; sudo journalctl -u $SERVICE -n 30 --no-pager -o cat" >&2 || true
    exit 3
}

# rollback_to NAME [AS_IT_WAS]: switch back to NAME and see it healthy. With
# AS_IT_WAS=1 (it was not healthy before the deploy either), NAME running
# again is as good as it gets.
rollback_to() {
    local prev="$1" as_it_was="${2:-0}"
    say "rolling back to $prev"
    LAST_BODY=""
    if install_unit "$prev" && switch_to "$prev"; then
        if wait_healthy "$(version_of "$prev")"; then
            info "$prev is running and healthy again"
            return 0
        elif [ "$as_it_was" -eq 1 ]; then
            warn "$prev is back, but not healthy: it was not healthy before the deploy either (last: ${LAST_BODY:-no answer})"
            return 0
        fi
    fi
    needs_person "ROLLBACK DID NOT COME UP HEALTHY"
}

# --------------------------------------------------------------------------
# Pre-flight. Everything here is read-only, locally and on the Pi.

require_tools

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

goarch_for "$remote_arch" || refuse "$REMOTE is '$remote_arch', which this script does not build for"
info "arch $remote_arch, service $active"
[ "$sudo_ok" = sudo-ok ] || refuse "sudo on $REMOTE needs a password; this script cannot type it"
[ "$CURRENT" != MISSING ] || refuse "\$HOME/$PI_DIR/$LINK is missing: this host was never set up (run ./setup-pi.sh)"
[ "$bin_link" = "${bin_link%"/$PI_DIR/$LINK"}/$PI_DIR/$LINK" ] ||
    refuse "$BIN_LINK points at '$bin_link', not ~/$PI_DIR/$LINK; switching that link would not change what runs"
[ "${free_kb:-0}" -gt 51200 ] || refuse "less than 50MB free in the home directory on $REMOTE"
info "running build: $CURRENT"

# unhealthy WHY: a deploy's rollback expects the old build back as healthy as
# it was, so with the broker down, say, a deploy can only fail and its
# rollback be judged failed too. Refused, unless this is a fix for the broken
# build (--allow-unhealthy), when the rollback accepts it back as it was. A
# rollback goes ahead: this may be why it is wanted.
PRE_HEALTHY=1
unhealthy() {
    PRE_HEALTHY=0
    if [ "$MODE" = rollback ]; then
        warn "$1"
    elif [ "$ALLOW_UNHEALTHY" -eq 1 ]; then
        warn "$1; deploying anyway (--allow-unhealthy)"
    elif [ "$CHECK" -eq 1 ]; then
        would_refuse "$1 (--allow-unhealthy to deploy a fix over it)"
    else
        refuse "$1. Deploy once it is, or deploy a fix over it with --allow-unhealthy"
    fi
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
    r "printf '%s\n' '$CURRENT' > ~/$PI_DIR/.previous" || refuse "cannot record $CURRENT as the build to come back to"
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
if commit_dirty; then
    if [ "$ALLOW_DIRTY" -eq 1 ]; then
        VERSION="$VERSION-dirty"
        warn "uncommitted changes: deploying as $VERSION"
    elif [ "$CHECK" -eq 1 ]; then
        would_refuse "uncommitted changes (commit first, or --allow-dirty)"
    else
        refuse "uncommitted changes. The version is the commit, so commit first (or --allow-dirty)"
    fi
fi
pushed=0
commit_pushed || pushed=$?
if [ $pushed -eq 2 ]; then
    if [ "$CHECK" -eq 1 ]; then
        would_refuse "cannot fetch from the remote to see that $VERSION is pushed"
    else
        refuse "cannot fetch from the remote, so cannot tell that $VERSION is still pushed"
    fi
elif [ $pushed -ne 0 ]; then
    if [ "$CHECK" -eq 1 ]; then
        would_refuse "$VERSION is not pushed"
    else
        refuse "$VERSION is not on any remote branch. Settle its date and push it first"
    fi
fi

vet_and_test

if [ "$CHECK" -eq 1 ]; then
    echo
    if [ ${#PROBLEMS[@]} -gt 0 ]; then
        say "check failed. A deploy would refuse:"
        for problem in "${PROBLEMS[@]}"; do info "$problem"; done
        exit 2
    fi
    say "check passed. A deploy would:"
    info "build $VERSION for $remote_arch and upload it as $SERVICE-<timestamp>-$VERSION"
    info "switch ~/$PI_DIR/$LINK from $CURRENT and restart $SERVICE"
    info "verify /healthz, the journal$([ $BROKER_CHECK -eq 1 ] && echo ' and the broker heartbeat'), or roll back"
    exit 0
fi

# --------------------------------------------------------------------------
# Build and upload. Nothing that runs changes until the confirmation.

build_for "$VERSION" "$remote_arch"

NEW="$SERVICE-$(date +%Y%m%d-%H%M%S)-$VERSION"
confirm "Upload $NEW to $REMOTE, switch $SERVICE to it from $CURRENT and restart?"

STAGE=".incoming-$NEW"
cleanup_stage() { r "rm -f ~/$PI_DIR/$STAGE ~/$PI_DIR/$NEW ~/$PI_DIR/$UNITS/$NEW.service" 2>/dev/null || true; }
# step_failed STEP: a step after the build, before anything that runs
# changed, failed. Say whether the link dropped or the step itself failed (a
# full disk, say), and tidy up if the link is there; a later deploy removes
# what this can't.
step_failed() {
    if r true 2>/dev/null; then
        cleanup_stage
        refuse "$1 failed on $REMOTE"
    fi
    refuse "lost $REMOTE while $1"
}
upload_build "$NEW" step_failed
since=$(r "date +%s") || step_failed "reading its clock"

# The unit file the running build runs under (if any: an old layout may have
# none here), and the new build's, beside their builds. Nothing that runs
# reads these.
r "mkdir -p ~/$PI_DIR/$UNITS && cd ~/$PI_DIR && { [ ! -f $SERVICE.service ] || cp $SERVICE.service $UNITS/$CURRENT.service; }" ||
    step_failed "recording the running build's unit file"
rsync -e "ssh ${SSHOPTS[*]}" "$SERVICE.service" "$REMOTE:$PI_DIR/$UNITS/$NEW.service" || step_failed "uploading the unit file"

# put_back_unit: after a failed install of the new unit, nothing has restarted:
# put the running build's unit back (or none, if it had none here). The
# reload is best effort: if the install's reload failed, systemd still has
# the old unit loaded.
put_back_unit() {
    r "cd ~/$PI_DIR && if [ -f $UNITS/$CURRENT.service ]; then
        cp $UNITS/$CURRENT.service .$SERVICE.service.tmp && mv .$SERVICE.service.tmp $SERVICE.service
    else rm -f $SERVICE.service; fi && { sudo systemctl daemon-reload || true; }"
}

# --------------------------------------------------------------------------
# Install the unit if changed, switch, restart, verify.

if ! install_unit "$NEW"; then
    put_back_unit || needs_person "installing the unit file of $NEW failed, and so did putting the old one back"
    cleanup_stage
    refuse "installing the unit file of $NEW failed (systemctl daemon-reload, or the link). The old unit file is back"
fi

failed=""
if ! { say "switching to $NEW and restarting" &&
    r "printf '%s\n' '$CURRENT' > ~/$PI_DIR/.previous" && switch_to "$NEW"; }; then
    failed="the restart failed"
elif ! wait_healthy "$VERSION"; then
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

# The new process's own output only: from $since the journal also holds the
# old build's shutdown. sudo, as the old script used: the deploy user may not
# read the system journal, and an unread journal must not pass. (Go's
# log.Fatal prints no "fatal"; the exit it causes fails the stay-up check.)
if [ -z "$failed" ]; then
    if ! journal=$(r "inv=\$(systemctl show -p InvocationID --value $SERVICE) && [ -n \"\$inv\" ] &&
        sudo journalctl _SYSTEMD_INVOCATION_ID=\$inv --no-pager -o cat"); then
        failed="cannot read the journal of $NEW"
    elif grep -qiE 'fatal|panic' <<<"$journal"; then
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
    r "sudo journalctl -u $SERVICE --since @$since --no-pager -o cat" 2>/dev/null | tail -20 | sed 's/^/   | /' >&2 || true
    r "printf '%s\n' '$NEW' > ~/$PI_DIR/.previous" || warn "could not record $NEW in .previous"
    rollback_to "$CURRENT" $((1 - PRE_HEALTHY))
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
# Uploads that a dropped link left staged go too, and the unit files of
# builds no longer there.
r "cd ~/$PI_DIR && rm -f .incoming-$SERVICE-* && ls -1t | grep '^$SERVICE-2' | tail -n +$((KEEP + 1)) | grep -vx -e '$NEW' -e '$CURRENT' | xargs -r rm -f -- &&
    for u in $UNITS/*.service; do [ ! -f \"\$u\" ] || [ -e \"\$(basename \"\$u\" .service)\" ] || rm -f \"\$u\"; done" ||
    warn "could not remove old builds from $REMOTE; the next deploy will"

echo
say "$SERVICE $VERSION is running on $REMOTE"
r "sudo journalctl -u $SERVICE --since @$since --no-pager -o cat" | tail -8 | sed 's/^/    /' || true
info "to undo: ./deploy.sh --rollback (back to $CURRENT)"
