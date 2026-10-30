#!/usr/bin/env bash
# Tests for deploy.sh and setup-pi.sh. Each case runs the real script in a
# throwaway git repo against stubs: ssh runs the "remote" commands locally with
# HOME pointed at a fake Pi home, and curl, mosquitto_sub, rsync, go,
# systemctl, journalctl, sudo, id and usermod are scripted from that fake Pi's
# state. No network, no Pi.
#
# Usage: ./deploy_test.sh
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
STUBS="$WORK/stubs"         # on the local PATH
RSTUBS="$WORK/remote-stubs" # on the "remote" PATH
mkdir -p "$STUBS" "$RSTUBS"

# ---------------------------------------------------------------- local stubs

cat >"$STUBS/ssh" <<'EOF'
#!/usr/bin/env bash
# ssh [-n] [-o opt]... host [command...]: run the command "on the Pi".
while [ $# -gt 0 ]; do
    case "$1" in
        -n) shift ;;
        -o) shift 2 ;;
        *) break ;;
    esac
done
shift # host
[ "${FAKE_SSH_DOWN:-0}" = 1 ] && { echo "ssh: connect: timed out" >&2; exit 255; }
# The link dropped (by the rsync stub): fail this call, and every later one
# unless it was only "once".
if [ -f "$FAKE_STATE/ssh_down" ]; then
    [ "$(cat "$FAKE_STATE/ssh_down")" = once ] && rm "$FAKE_STATE/ssh_down"
    echo "ssh: connect: no route to host" >&2
    exit 255
fi
cd "$FAKE_HOME"
HOME="$FAKE_HOME" PATH="$RSTUBS:$PATH" exec bash -c "$*"
EOF

cat >"$STUBS/rsync" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_RSYNC_FAIL:-0}" = 1 ] && exit 12
src="${*: -2:1}" dest="${*: -1}"
# FAKE_RSYNC_FAIL_ONCE=1: the first upload of a build fails.
case "$dest" in *.incoming-*)
    if [ "${FAKE_RSYNC_FAIL_ONCE:-0}" = 1 ] && [ ! -f "$FAKE_STATE/rsync_failed" ]; then
        touch "$FAKE_STATE/rsync_failed"
        exit 12
    fi ;;
esac
cp "$src" "$FAKE_HOME/${dest#*:}"
# FAKE_UNIT_RSYNC_FAIL=1: a unit file arrives, then rsync fails anyway.
case "$dest" in *.service) [ "${FAKE_UNIT_RSYNC_FAIL:-0}" = 1 ] && exit 12 ;; esac
# FAKE_SSH_DROP_AFTER_UPLOAD=once|stay: the link drops once the binary is up.
case "$dest" in *.incoming-*) [ -n "${FAKE_SSH_DROP_AFTER_UPLOAD:-}" ] && echo "$FAKE_SSH_DROP_AFTER_UPLOAD" >"$FAKE_STATE/ssh_down" ;; esac
true
EOF

cat >"$STUBS/go" <<'EOF'
#!/usr/bin/env bash
case "$1" in
    vet) exit "${FAKE_VET_RC:-0}" ;;
    test) exit "${FAKE_TEST_RC:-0}" ;;
    build)
        out="" ver=""
        while [ $# -gt 0 ]; do
            case "$1" in
                -o) out="$2"; shift 2 ;;
                -ldflags) ver="${2##*main.version=}"; shift 2 ;;
                *) shift ;;
            esac
        done
        if [ "${FAKE_TINY_BUILD:-0}" = 1 ]; then echo "FAKEBIN version=$ver" >"$out"; exit 0; fi
        { echo "FAKEBIN version=$ver"; head -c 1100000 /dev/zero; } >"$out" ;;
esac
EOF

# The Pi's HTTP server, answering for whichever build systemctl last started.
cat >"$STUBS/curl" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_HTTP_DOWN:-0}" = 1 ] && exit 7
url="${*: -1}" path="/${url#http://*/}"
running=$(cat "$FAKE_STATE/running")
[ "$running" = DOWN ] && exit 7
ver=$(head -1 "$FAKE_HOME/boiler-sensor/$running" | sed -n 's/^FAKEBIN version=//p')
started=$(cat "$FAKE_STATE/started")
if [ "${FAKE_CRASHLOOP:-0}" = 1 ] && [ -n "$ver" ]; then
    n=$(($(cat "$FAKE_STATE/calls" 2>/dev/null || echo 0) + 1)); echo $n >"$FAKE_STATE/calls"
    started="2026-10-30T10:00:0${n: -1}Z"
fi
ch=$(cat "$FAKE_STATE/ch") hw=$(cat "$FAKE_STATE/hw")
mqtt=true ready=true
[ "${FAKE_MQTT_DOWN:-0}" = 1 ] && mqtt=false
# FAKE_OLD_BROKEN=1: the legacy build never baselines; a new build is fine.
[ "${FAKE_OLD_BROKEN:-0}" = 1 ] && [ -z "$ver" ] && ready=false
case "$path" in
    /index.json)
        printf '{"status":{"ch":"%s","hw":"%s","ready":%s,"uptime_seconds":5,"start_time":"%s","mqtt":{"connected":%s,"broker":"tcp://127.0.0.1:1883"}}}\n' "$ch" "$hw" "$ready" "$started" "$mqtt" ;;
    /healthz)
        [ -n "$ver" ] || exit 22 # a pre-/healthz build: 404
        status=ok
        [ "$mqtt" = true ] || status=degraded
        [ "${FAKE_NEW_BROKEN:-0}" = 1 ] && status=starting
        printf '{"status":"%s","version":"%s","start_time":"%s","ready":true,"mqtt_connected":%s,"channels":{"ch":"%s","hw":"%s"}}\n' \
            "$status" "$ver" "$started" "$mqtt" "$ch" "$hw" ;;
    *) exit 22 ;;
esac
EOF

cat >"$STUBS/mosquitto_sub" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_NO_BROKER:-0}" = 1 ] && exit 5
started=$(cat "$FAKE_STATE/started")
[ "${FAKE_STALE_HB:-0}" = 1 ] && started="2026-07-07T06:14:53Z"
printf '{"status":{"event":"HEARTBEAT","start_time":"%s"}}\n' "$started"
EOF

# ---------------------------------------------------------------- remote stubs

cat >"$RSTUBS/sudo" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -n ] && shift
[ "${FAKE_SUDO_PW:-0}" = 1 ] && exit 1
FAKE_AS_ROOT=1 exec "$@"
EOF

cat >"$RSTUBS/systemctl" <<'EOF'
#!/usr/bin/env bash
echo "systemctl $*" >>"$FAKE_STATE/log"
case "$1" in
    is-active) cat "$FAKE_STATE/active" ;;
    is-enabled) cat "$FAKE_STATE/enabled" ;;
    enable) echo enabled >"$FAKE_STATE/enabled" ;;
    show) cat "$FAKE_STATE/invocation" ;; # show -p InvocationID --value
    daemon-reload)
        # FAKE_RELOAD_FAIL=1: the first daemon-reload fails; =always: every one.
        [ "${FAKE_RELOAD_FAIL:-0}" = always ] && exit 1
        if [ "${FAKE_RELOAD_FAIL:-0}" = 1 ] && [ ! -f "$FAKE_STATE/reload_failed" ]; then
            touch "$FAKE_STATE/reload_failed"
            exit 1
        fi ;;
    restart | start)
        target=$(basename "$(readlink "$HOME/boiler-sensor/boiler-sensor-arm")")
        # FAKE_RESTART_FAIL=GLOB: starting a build that matches fails.
        # shellcheck disable=SC2053 # a glob on purpose
        if [ -n "${FAKE_RESTART_FAIL:-}" ] && [[ "$target" == $FAKE_RESTART_FAIL ]]; then
            echo DOWN >"$FAKE_STATE/running"
            echo "Job for boiler-sensor.service failed." >&2
            exit 1
        fi
        # The old process may log on its way out, under its own invocation.
        old=$(cat "$FAKE_STATE/invocation")
        [ -n "${FAKE_OLD_STOP_LOG:-}" ] && printf '%s %s\n' "$old" "$FAKE_OLD_STOP_LOG" >>"$FAKE_STATE/journal"
        echo "$target" >"$FAKE_STATE/running"
        echo active >"$FAKE_STATE/active"
        inv="inv$(wc -l <"$FAKE_STATE/log" | tr -d ' ')"
        echo "$inv" >"$FAKE_STATE/invocation"
        echo "$inv boiler-sensor $target starting" >>"$FAKE_STATE/journal"
        [ "${FAKE_PANIC:-0}" = 1 ] && echo "$inv panic: runtime error: index out of range" >>"$FAKE_STATE/journal"
        n=$(wc -l <"$FAKE_STATE/log" | tr -d ' ')
        printf '2026-10-30T10:%02d:00Z\n' "$n" >"$FAKE_STATE/started" ;;
esac
EOF

cat >"$RSTUBS/journalctl" <<'EOF'
#!/usr/bin/env bash
# Lines in $FAKE_STATE/journal are "<invocation> <message>". --since is
# ignored: every line here is "since" a deploy's start.
[ "${FAKE_JOURNAL_BROKEN:-0}" = 1 ] && { echo "journal is corrupt" >&2; exit 1; }
if [ "${FAKE_JOURNAL_NEEDS_ROOT:-0}" = 1 ] && [ "${FAKE_AS_ROOT:-0}" != 1 ]; then
    echo "No journal files were opened due to insufficient permissions." >&2
    exit 1
fi
want=""
for a in "$@"; do case "$a" in _SYSTEMD_INVOCATION_ID=*) want="${a#*=}" ;; esac; done
while read -r inv msg; do
    [ -z "$want" ] || [ "$inv" = "$want" ] && echo "$msg"
done <"$FAKE_STATE/journal"
true
EOF

cat >"$RSTUBS/id" <<'EOF'
#!/usr/bin/env bash
# id -nG
if [ "$(cat "$FAKE_STATE/gpio")" = yes ]; then echo "pi adm gpio"; else echo "pi adm"; fi
EOF

cat >"$RSTUBS/usermod" <<'EOF'
#!/usr/bin/env bash
echo "usermod $*" >>"$FAKE_STATE/log"
[ "$1 $2" = "-aG gpio" ] && echo yes >"$FAKE_STATE/gpio"
EOF

cat >"$RSTUBS/mv" <<'EOF'
#!/usr/bin/env bash
# FAKE_DISK_FULL=1: moves fail, as on a full disk.
[ "${FAKE_DISK_FULL:-0}" = 1 ] && { echo "mv: cannot move: No space left on device" >&2; exit 1; }
PATH="${PATH#*:}" exec mv "$@"
EOF

cat >"$RSTUBS/uname" <<'EOF'
#!/usr/bin/env bash
echo "${FAKE_ARCH:-armv6l}"
EOF

cat >"$RSTUBS/sha256sum" <<'EOF'
#!/usr/bin/env bash
if [ "${FAKE_CORRUPT:-0}" = 1 ]; then echo "0000  $1"; else shasum -a 256 "$@"; fi
EOF

chmod +x "$STUBS"/* "$RSTUBS"/*

# ---------------------------------------------------------------- fixtures

LEGACY=boiler-sensor-20260323-163645

# fresh: a new fake Pi running the legacy build, and a clean pushed checkout.
fresh() {
    rm -rf "$WORK/case"
    mkdir -p "$WORK/case"/{home/boiler-sensor,state,bin,etc}
    FAKE_HOME="$WORK/case/home" FAKE_STATE="$WORK/case/state"
    local pi="$FAKE_HOME/boiler-sensor"
    for b in boiler-sensor-20260309-115805 boiler-sensor-20260320-093441 "$LEGACY"; do
        echo LEGACY >"$pi/$b"
        sleep 0.01 # distinct mtimes for ls -t
    done
    ln -s "$LEGACY" "$pi/boiler-sensor-arm"
    cp "$HERE/boiler-sensor.service" "$pi/boiler-sensor.service"
    ln -s "$pi/boiler-sensor-arm" "$WORK/case/bin/boiler-sensor"
    ln -s "$pi/boiler-sensor.service" "$WORK/case/etc/boiler-sensor.service"
    echo active >"$FAKE_STATE/active"
    echo enabled >"$FAKE_STATE/enabled"
    echo yes >"$FAKE_STATE/gpio"
    echo "$LEGACY" >"$FAKE_STATE/running"
    echo 2026-07-07T06:14:53Z >"$FAKE_STATE/started"
    echo OFF >"$FAKE_STATE/ch"
    echo OFF >"$FAKE_STATE/hw"
    : >"$FAKE_STATE/log"
    echo inv0 >"$FAKE_STATE/invocation"
    echo "inv0 boiler-sensor $LEGACY running" >"$FAKE_STATE/journal"

    REPO="$WORK/case/repo"
    git init -q --bare "$WORK/case/origin.git"
    git init -q -b main "$REPO"
    mkdir -p "$REPO/deploy"
    cp "$HERE/deploy.sh" "$HERE/setup-pi.sh" "$HERE/boiler-sensor.service" "$HERE/.gitignore" "$REPO/"
    cp "$HERE/deploy/lib.sh" "$REPO/deploy/"
    mkdir -p "$REPO/cmd/boiler-sensor"
    echo 'package main' >"$REPO/cmd/boiler-sensor/main.go"
    echo 'module x' >"$REPO/go.mod"
    git -C "$REPO" add -A
    git -C "$REPO" -c user.name=t -c user.email=t@t commit -qm fixture
    git -C "$REPO" remote add origin "$WORK/case/origin.git"
    git -C "$REPO" push -q origin main
    VERSION=$(git -C "$REPO" rev-parse --short=7 HEAD)
}

# path_without TOOL: a PATH (stubs first) with every command but TOOL.
path_without() {
    local dir="$WORK/path-without-$1" d f
    if [ ! -d "$dir" ]; then
        mkdir -p "$dir"
        for d in "$STUBS" ${PATH//:/ }; do
            [ -d "$d" ] || continue
            for f in "$d"/*; do
                [ -x "$f" ] && [ "${f##*/}" != "$1" ] && [ ! -e "$dir/${f##*/}" ] && ln -s "$f" "$dir/${f##*/}"
            done
        done
    fi
    echo "$dir"
}

# bare: a fresh Pi with nothing of boiler-sensor on it, and the same checkout.
bare() {
    fresh
    rm -rf "$FAKE_HOME/boiler-sensor" "$WORK/case/bin/boiler-sensor" "$WORK/case/etc/boiler-sensor.service"
    echo DOWN >"$FAKE_STATE/running"
    echo inactive >"$FAKE_STATE/active"
    echo not-found >"$FAKE_STATE/enabled"
    echo no >"$FAKE_STATE/gpio"
}

pass=0 fail=0
OUT="" RC=0

# run SCRIPT [VAR=value...] -- [args...]; stdin is passed through.
run() {
    local script="$1" envs=()
    shift
    while [ $# -gt 0 ] && [ "$1" != "--" ]; do envs+=("$1"); shift; done
    [ $# -gt 0 ] && shift
    OUT=$(env PATH="$STUBS:$PATH" RSTUBS="$RSTUBS" FAKE_HOME="$FAKE_HOME" FAKE_STATE="$FAKE_STATE" \
        DEPLOY_BIN_LINK="$WORK/case/bin/boiler-sensor" DEPLOY_UNIT_LINK="$WORK/case/etc/boiler-sensor.service" \
        DEPLOY_HTTP_HOST=pi.test DEPLOY_VERIFY_TIMEOUT=3 DEPLOY_SETTLE=0 ${envs[@]+"${envs[@]}"} \
        "$REPO/$script" "$@" 2>&1) && RC=0 || RC=$?
}
deploy() { run deploy.sh "$@"; }
setup() { run setup-pi.sh "$@"; }

# count GLOB: how many entries in the fake Pi's ~/boiler-sensor match.
count() { find "$FAKE_HOME/boiler-sensor" -mindepth 1 -maxdepth 1 -name "$1" | wc -l | tr -d ' '; }
link() { readlink "$FAKE_HOME/boiler-sensor/boiler-sensor-arm"; }
restarts() { grep -c 'systemctl restart' "$FAKE_STATE/log" || true; }
# changes: how many systemctl/usermod calls changed something on the fake Pi.
changes() { grep -cvE 'systemctl (is-active|is-enabled|show)' "$FAKE_STATE/log" || true; }
reloads() { grep -c 'systemctl daemon-reload' "$FAKE_STATE/log" || true; }
unit_is() { cmp -s "$FAKE_HOME/boiler-sensor/boiler-sensor.service" "$1"; }
# change_unit: commit and push a change to the unit file.
change_unit() {
    echo "# changed" >>"$REPO/boiler-sensor.service"
    git -C "$REPO" -c user.name=t -c user.email=t@t commit -qam unit
    git -C "$REPO" push -q origin main
    VERSION=$(git -C "$REPO" rev-parse --short=7 HEAD)
}

# pi_link PATH: where a symlink on the fake Pi points, or MISSING.
pi_link() { readlink "$1" || echo MISSING; }
# orphan_units_none: every kept unit file belongs to a build still on the Pi.
orphan_units_none() {
    local u
    for u in "$FAKE_HOME/boiler-sensor/.units"/*.service; do
        [ -e "$u" ] || continue
        [ -e "$FAKE_HOME/boiler-sensor/$(basename "$u" .service)" ] || return 1
    done
}
nothing_set_up() {
    [ ! -e "$FAKE_HOME/boiler-sensor" ] && [ "$(pi_link "$WORK/case/bin/boiler-sensor")" = MISSING ] &&
        [ "$(pi_link "$WORK/case/etc/boiler-sensor.service")" = MISSING ] && [ "$(cat "$FAKE_STATE/enabled")" = not-found ]
}

check() { # check NAME CONDITION...
    local name="$1"; shift
    if "$@"; then
        echo "ok   $name"; pass=$((pass + 1))
    else
        echo "FAIL $name"
        printf '%s\n' "$OUT" | sed 's/^/     | /'
        fail=$((fail + 1))
    fi
}
rc_is() { [ "$RC" -eq "$1" ]; }
says() { grep -qE -- "$1" <<<"$OUT"; }
link_is() { [ "$(link)" = "$1" ]; }
new_build_is_running() { [[ "$(link)" == boiler-sensor-2*-"$VERSION" ]] && [ "$(cat "$FAKE_STATE/running")" = "$(link)" ]; }

# ---------------------------------------------------------------- cases

fresh
deploy -- --check
check "--check passes on a healthy Pi" rc_is 0
check "--check reports the running build and state" says "running build: $LEGACY"
check "--check reports state and broker" says 'state: ch=OFF hw=OFF ready=true mqtt=true'
check "--check changes nothing" link_is "$LEGACY"
check "--check restarts nothing" [ "$(restarts)" -eq 0 ]
check "--check uploads nothing" [ "$(count '*')" -eq 5 ]

fresh
deploy -- --yes
check "deploy --yes succeeds" rc_is 0
check "deploy switches to the new build, named by commit" new_build_is_running
check "deploy records the previous build" [ "$(cat "$FAKE_HOME/boiler-sensor/.previous")" = "$LEGACY" ]
check "deploy restarts once" [ "$(restarts)" -eq 1 ]
check "deploy confirms the broker heartbeat" says 'broker holds a HEARTBEAT from this run'
check "deploy leaves no staging file" [ "$(count '.incoming-*')" -eq 0 ]
check "deploy keeps 3 builds" [ "$(count 'boiler-sensor-2*')" -eq 3 ]
check "deploy keeps the previous build" [ -f "$FAKE_HOME/boiler-sensor/$LEGACY" ]
check "deploy leaves the checkout clean (bin/ is ignored)" [ -z "$(git -C "$REPO" status --porcelain)" ]
check "unit file unchanged: no daemon-reload" [ "$(grep -c daemon-reload "$FAKE_STATE/log" || true)" -eq 0 ]
first=$(link)

sleep 1 # a new timestamp in the build name
deploy -- --yes
check "a second deploy of the same commit succeeds" rc_is 0
check "it keeps the first deploy as previous" [ "$(cat "$FAKE_HOME/boiler-sensor/.previous")" = "$first" ]
check "it still keeps 3 builds" [ "$(count 'boiler-sensor-2*')" -eq 3 ]

deploy -- --rollback --yes
check "--rollback succeeds" rc_is 0
check "--rollback switches to the previous build" link_is "$first"

fresh
deploy FAKE_NEW_BROKEN=1 -- --yes
check "an unhealthy new build exits 1" rc_is 1
check "an unhealthy new build is rolled back" link_is "$LEGACY"
check "the rolled-back build is running" [ "$(cat "$FAKE_STATE/running")" = "$LEGACY" ]
check "it says what failed" says 'FAILED VERIFICATION: /healthz did not report'

fresh
deploy FAKE_RESTART_FAIL="*-$VERSION" -- --yes
check "a restart that fails is rolled back" link_is "$LEGACY"
check "the old build runs again" [ "$(cat "$FAKE_STATE/running")" = "$LEGACY" ]
check "and exits 1" rc_is 1
check "it says the restart failed" says 'FAILED VERIFICATION: the restart failed'

fresh
deploy FAKE_RESTART_FAIL="*" -- --yes
check "a rollback whose restart fails too needs a person" rc_is 3
check "and says so" says 'needs a person'

fresh
deploy FAKE_CRASHLOOP=1 -- --yes
check "a crash-looping build is rolled back" link_is "$LEGACY"
check "it says it did not stay up" says 'did not stay up'

fresh
deploy FAKE_PANIC=1 -- --yes
check "a panic in the journal triggers rollback" link_is "$LEGACY"
check "it quotes the panic" says 'panic: runtime error'

fresh
deploy FAKE_PANIC=1 FAKE_JOURNAL_NEEDS_ROOT=1 -- --yes
check "the journal is read with sudo: a panic is still caught" link_is "$LEGACY"
check "and quoted" says 'panic: runtime error'

fresh
deploy FAKE_JOURNAL_BROKEN=1 -- --yes
check "a journal that cannot be read fails verification" rc_is 1
check "it says the journal could not be read" says 'cannot read the journal'

fresh
deploy FAKE_OLD_STOP_LOG="mqtt: fatal: connection lost while stopping" -- --yes
check "the old build's shutdown output does not fail the new build" rc_is 0
check "the new build stays" new_build_is_running

fresh
deploy FAKE_STALE_HB=1 -- --yes
check "a stale broker heartbeat triggers rollback" link_is "$LEGACY"
check "it explains the broker check" says 'not a HEARTBEAT from this run'

fresh
deploy FAKE_MQTT_DOWN=1 -- --yes
check "a running build without MQTT is refused before the deploy" rc_is 2
check "it says the running build is not healthy" says 'running build is not healthy'
check "and restarts nothing" [ "$(restarts)" -eq 0 ]
deploy FAKE_MQTT_DOWN=1 -- --check
check "--check fails on it too" rc_is 2
check "but keeps checking: it still runs vet and test" says '==> go vet and go test'
check "and ends saying a deploy would refuse" says 'a deploy would refuse'

fresh
deploy FAKE_OLD_BROKEN=1 -- --yes
check "a broken running build is refused by default" rc_is 2
check "the refusal names the override" says '--allow-unhealthy'
deploy FAKE_OLD_BROKEN=1 -- --allow-unhealthy --yes
check "--allow-unhealthy deploys a fix over a broken build" rc_is 0
check "the fix runs" new_build_is_running

fresh
deploy FAKE_MQTT_DOWN=1 -- --allow-unhealthy --yes
check "--allow-unhealthy with the broker down: the new build is rolled back (1, not 3)" rc_is 1
check "the old build is back" link_is "$LEGACY"
check "accepted as it was before" says 'was not healthy before the deploy either'

fresh
deploy FAKE_HTTP_DOWN=1 -- --yes
check "a running build that does not answer is refused" rc_is 2
check "and restarts nothing (no answer)" [ "$(restarts)" -eq 0 ]

fresh
deploy -- --yes
deploy FAKE_MQTT_DOWN=1 -- --check --rollback
check "an unhealthy running build does not block --rollback" rc_is 0

fresh
deploy FAKE_NO_BROKER=1 -- --yes
check "an unreachable broker skips that check, with a warning" says 'broker check will be skipped'
check "and the deploy still succeeds" rc_is 0

fresh
echo "// edit" >>"$REPO/cmd/boiler-sensor/main.go"
deploy -- --yes
check "uncommitted changes are refused" rc_is 2
check "a refused deploy changes nothing" link_is "$LEGACY"
deploy -- --check
check "--check reports them" says 'a deploy would refuse'
check "and fails" rc_is 2

fresh
git -C "$REPO" -c user.name=t -c user.email=t@t commit -q --allow-empty -m unpushed
deploy -- --yes
check "an unpushed commit is refused" rc_is 2
check "it says to push" says 'not on any remote branch'

fresh
# Elsewhere the commit's date is rewritten and force-pushed; this checkout's
# origin/main still names the old hash.
git clone -q "$WORK/case/origin.git" "$WORK/case/other"
GIT_COMMITTER_DATE="2026-10-29T09:00:00Z" git -C "$WORK/case/other" -c user.name=t -c user.email=t@t \
    commit -q --amend --no-edit --date=2026-10-29T09:00:00Z
git -C "$WORK/case/other" push -q -f origin main
deploy -- --yes
check "a commit force-pushed away is refused, though origin/main is stale" rc_is 2
check "it says the commit is not on the remote" says 'not on any remote branch'

fresh
git -C "$REPO" remote set-url origin "$WORK/case/nowhere.git"
deploy -- --yes
check "a remote that cannot be fetched is refused" rc_is 2
check "it says so" says 'cannot fetch'

fresh
deploy FAKE_TEST_RC=1 -- --yes
check "failing tests are refused" rc_is 2
check "before anything is uploaded" [ "$(count '*')" -eq 5 ]

for tool in rsync shasum; do
    fresh
    deploy PATH="$(path_without $tool)" -- --yes
    check "a missing $tool is refused" rc_is 2
    check "by name, before anything is uploaded" says "$tool not found"
    check "leaving the Pi untouched" [ "$(count '*')" -eq 5 ]
done

fresh
deploy FAKE_ARCH=x86_64 -- --yes
check "an unknown architecture is refused" says "is 'x86_64'"

fresh
deploy FAKE_SUDO_PW=1 -- --yes
check "sudo needing a password is refused" says 'needs a password'

fresh
deploy FAKE_SSH_DOWN=1 -- --check
check "an unreachable Pi is refused" rc_is 2

fresh
deploy FAKE_CORRUPT=1 -- --yes
check "a corrupted upload is refused" says 'upload arrived corrupted'
check "and nothing is switched" link_is "$LEGACY"
check "and the staged file is removed" [ "$(count '.incoming-*')" -eq 0 ]

fresh
deploy FAKE_SSH_DROP_AFTER_UPLOAD=once -- --yes
check "a dropped link after the upload is refused (2, not ssh's 255)" rc_is 2
check "it says what it was doing" says 'REFUSED: checking its sha256 failed on '
check "nothing is switched after the drop" link_is "$LEGACY"
check "the staged file is removed once the link is back" [ "$(count '.incoming-*')" -eq 0 ]
check "and so is any half-installed build" [ "$(count 'boiler-sensor-2*')" -eq 3 ]

fresh
deploy FAKE_DISK_FULL=1 -- --yes
check "a remote step that fails with the link up is refused" rc_is 2
check "named by the step, not blamed on the link" says 'REFUSED: moving it into place failed on '
check "not 'lost'" eval '! says "REFUSED: lost"'

fresh
deploy FAKE_SSH_DROP_AFTER_UPLOAD=stay -- --yes
check "a link that stays down after the upload is refused too" rc_is 2
check "and that one says the link was lost" says 'REFUSED: lost .* while checking its sha256'
check "the staged file cannot be removed then" [ "$(count '.incoming-*')" -eq 1 ]
rm "$FAKE_STATE/ssh_down"
sleep 1 # another build name, so another staging name
deploy -- --yes
check "the next deploy succeeds" rc_is 0
check "and removes the stale staged file" [ "$(count '.incoming-*')" -eq 0 ]

fresh
OUT=$(echo n | env PATH="$STUBS:$PATH" RSTUBS="$RSTUBS" FAKE_HOME="$FAKE_HOME" FAKE_STATE="$FAKE_STATE" \
    DEPLOY_BIN_LINK="$WORK/case/bin/boiler-sensor" DEPLOY_HTTP_HOST=pi.test "$REPO/deploy.sh" 2>&1) && RC=0 || RC=$?
check "answering no at the prompt declines" rc_is 2
check "a declined deploy uploads and changes nothing" [ "$(count '*')" -eq 5 ]

fresh
change_unit
deploy -- --yes
check "a changed unit file is installed" says 'installed the unit file of'
check "and systemd reloaded" [ "$(reloads)" -eq 1 ]
check "the new unit is in place" unit_is "$REPO/boiler-sensor.service"
deploy -- --rollback --yes
check "--rollback puts the old unit file back" unit_is "$HERE/boiler-sensor.service"
check "and reloads systemd" [ "$(reloads)" -eq 2 ]
deploy -- --rollback --yes
check "--rollback again brings the new unit back" unit_is "$REPO/boiler-sensor.service"
check "--rollback again runs the new build" new_build_is_running

fresh
change_unit
deploy FAKE_NEW_BROKEN=1 -- --yes
check "a failed deploy with a new unit file is rolled back" rc_is 1
check "the old unit file is back" unit_is "$HERE/boiler-sensor.service"
check "and systemd reloaded with it" [ "$(reloads)" -eq 2 ]
check "and the old build runs" [ "$(cat "$FAKE_STATE/running")" = "$LEGACY" ]

OLDER=boiler-sensor-20260320-093441
for how in 1 always; do
    fresh
    echo "$OLDER" >"$FAKE_HOME/boiler-sensor/.previous"
    change_unit
    deploy FAKE_RELOAD_FAIL=$how -- --yes
    check "daemon-reload failing ($how): refused, not rolled back (exit 2)" rc_is 2
    check "daemon-reload failing ($how): it says so" says 'daemon-reload'
    check "daemon-reload failing ($how): the old unit file is back" unit_is "$HERE/boiler-sensor.service"
    check "daemon-reload failing ($how): .previous is left alone" [ "$(cat "$FAKE_HOME/boiler-sensor/.previous")" = "$OLDER" ]
    check "daemon-reload failing ($how): no restart" [ "$(restarts)" -eq 0 ]
    check "daemon-reload failing ($how): the old build runs" link_is "$LEGACY"
done

fresh
echo "$OLDER" >"$FAKE_HOME/boiler-sensor/.previous"
change_unit
deploy FAKE_UNIT_RSYNC_FAIL=1 -- --yes
check "a unit upload that fails after landing is refused" rc_is 2
check "the unit file on the Pi is still the old one" unit_is "$HERE/boiler-sensor.service"
check ".previous is left alone after the failed unit upload" [ "$(cat "$FAKE_HOME/boiler-sensor/.previous")" = "$OLDER" ]
check "nothing restarted after the failed unit upload" [ "$(restarts)" -eq 0 ]

fresh
for _ in 1 2 3; do
    deploy -- --yes
    sleep 1 # a new timestamp in the build name
done
check "three deploys later the legacy build is cleaned out" [ ! -e "$FAKE_HOME/boiler-sensor/$LEGACY" ]
check "and its unit file with it" orphan_units_none
check "one unit file per kept build" [ "$(find "$FAKE_HOME/boiler-sensor/.units" -name '*.service' | wc -l | tr -d ' ')" -eq 3 ]

fresh
rm "$FAKE_HOME/boiler-sensor/boiler-sensor.service"
deploy -- --yes
check "a Pi without ~/boiler-sensor/boiler-sensor.service can be deployed to" rc_is 0
check "and gets the unit file" unit_is "$HERE/boiler-sensor.service"
check "and runs the new build" new_build_is_running

fresh
deploy -- --yes
deploy -- --rollback --yes
check "--rollback with the unit unchanged does not reload systemd" [ "$(reloads)" -eq 0 ]

fresh
deploy -- --yes
deployed=$(link)
for order in "--check --rollback" "--rollback --check"; do
    # shellcheck disable=SC2086 # split the flags on purpose
    deploy -- $order --yes
    check "$order is read-only: exits 0" rc_is 0
    check "$order is read-only: the link is unchanged" link_is "$deployed"
    check "$order is read-only: no restart" [ "$(restarts)" -eq 1 ]
    check "$order says what a rollback would do" says "A rollback would switch .* back to $LEGACY"
done

fresh
deploy -- --rollback --yes
check "--rollback with nothing recorded is refused" says 'nothing recorded'

# ---------------------------------------------------------------- deploy/lib.sh

# lib CODE: run CODE with deploy/lib.sh sourced; its output in OUT. CODE is
# single-quoted on purpose (SC2016): it expands in there.
lib() { OUT=$(bash -c "set -euo pipefail; source '$HERE/deploy/lib.sh'; $1" 2>&1) && RC=0 || RC=$?; }
# shellcheck disable=SC2016
lib 'goarch_for armv6l && echo "$GOARCH $GOARM"'
check "lib: armv6l builds as arm 6" [ "$OUT" = "arm 6" ]
# shellcheck disable=SC2016
lib 'goarch_for aarch64 && echo "$GOARCH [$GOARM]"'
check "lib: aarch64 builds as arm64" [ "$OUT" = "arm64 []" ]
lib 'goarch_for x86_64'
check "lib: x86_64 is not built for" rc_is 1
lib "jget status.mqtt.connected <<<'{\"status\":{\"mqtt\":{\"connected\":true}}}'"
check "lib: jget reads a nested bool as true" [ "$OUT" = true ]
lib "jget status.nope <<<'not json'"
check "lib: jget is empty on junk" [ -z "$OUT" ]
lib 'version_of boiler-sensor-20261030-124322-3c0d98c; echo; version_of boiler-sensor-20260323-163645'
check "lib: version_of reads the stamp, empty for legacy builds" [ "$OUT" = "3c0d98c" ]

# ---------------------------------------------------------------- setup-pi.sh

bare
deploy -- --check
check "deploy on a bare Pi is refused" rc_is 2
check "and points at setup-pi.sh" says 'setup-pi.sh'

bare
setup -- --check
check "setup --check on a bare Pi passes" rc_is 0
check "it lists what it would do" says 'Setup would'
check "and that includes a first build" says "build $VERSION"
check "setup --check changes nothing" nothing_set_up

bare
setup -- --yes
check "setup --yes on a bare Pi succeeds" rc_is 0
check "it links a first build, named by commit" new_build_is_running
check "it installs the unit file" unit_is "$HERE/boiler-sensor.service"
check "it links /usr/local/bin" [ "$(pi_link "$WORK/case/bin/boiler-sensor")" = "$FAKE_HOME/boiler-sensor/boiler-sensor-arm" ]
check "it links the systemd unit" [ "$(pi_link "$WORK/case/etc/boiler-sensor.service")" = "$FAKE_HOME/boiler-sensor/boiler-sensor.service" ]
check "it adds the user to gpio" [ "$(cat "$FAKE_STATE/gpio")" = yes ]
check "it enables the service" [ "$(cat "$FAKE_STATE/enabled")" = enabled ]
check "it reloads systemd" [ "$(reloads)" -eq 1 ]
check "it leaves no staging file" [ "$(count '.incoming-*')" -eq 0 ]
check "it checks the service came up" says 'is running on'
before=$(changes)
setup -- --yes
check "setup again succeeds" rc_is 0
check "and has nothing to do" says 'already set up'
check "and changes nothing on the Pi" [ "$(changes)" -eq "$before" ]
deploy -- --check
check "deploy --check passes on a Pi setup made" rc_is 0

fresh
setup -- --check
check "setup --check on a set-up Pi has nothing to do" says 'already set up'

fresh
# Set up by hand once: a build linked, the rest not.
rm "$WORK/case/bin/boiler-sensor" "$WORK/case/etc/boiler-sensor.service"
echo inactive >"$FAKE_STATE/active"
echo DOWN >"$FAKE_STATE/running"
setup -- --yes
check "setup finishes a half-set-up Pi" rc_is 0
check "without building: the existing build is kept" link_is "$LEGACY"
check "and starts it" [ "$(cat "$FAKE_STATE/running")" = "$LEGACY" ]

bare
mkdir -p "$WORK/case/bin" && ln -s /opt/other/boiler-sensor "$WORK/case/bin/boiler-sensor"
setup -- --yes
check "a /usr/local/bin entry pointing elsewhere is refused" rc_is 2
check "it names it" says '/opt/other/boiler-sensor'
check "and is left alone" [ "$(pi_link "$WORK/case/bin/boiler-sensor")" = /opt/other/boiler-sensor ]
check "nothing else is done" [ ! -e "$FAKE_HOME/boiler-sensor" ]

bare
git -C "$REPO" -c user.name=t -c user.email=t@t commit -q --allow-empty -m unpushed
setup -- --yes
check "setup refuses to build an unpushed commit" rc_is 2
check "and says why" says 'not on any remote branch'
check "having changed nothing" nothing_set_up

for script in deploy setup; do
    bare
    [ $script = deploy ] && fresh
    $script FAKE_RSYNC_FAIL_ONCE=1 -- --yes
    check "$script retries a failed upload" rc_is 0
    check "$script says it retried" says 'upload attempt 1 failed'
    bare
    [ $script = deploy ] && fresh
    $script FAKE_TINY_BUILD=1 -- --yes
    check "$script refuses a build too small to be real" rc_is 2
    check "$script says how small" says 'the build is only'
done

bare
setup FAKE_SUDO_PW=1 -- --yes
check "setup refuses without passwordless sudo" rc_is 2

bare
OUT=$(echo n | env PATH="$STUBS:$PATH" RSTUBS="$RSTUBS" FAKE_HOME="$FAKE_HOME" FAKE_STATE="$FAKE_STATE" \
    DEPLOY_BIN_LINK="$WORK/case/bin/boiler-sensor" DEPLOY_UNIT_LINK="$WORK/case/etc/boiler-sensor.service" \
    DEPLOY_HTTP_HOST=pi.test "$REPO/setup-pi.sh" 2>&1) && RC=0 || RC=$?
check "answering no declines setup" rc_is 2
check "a declined setup changes nothing" nothing_set_up

bare
setup FAKE_MQTT_DOWN=1 -- --yes
check "setup whose service does not come up healthy exits 1" rc_is 1
check "and says so" says 'did not come up healthy'

bare
setup FAKE_RESTART_FAIL="*" -- --yes
check "a setup step that fails exits 3" rc_is 3
check "and says to re-run" says 're-run'

fresh
deploy -- --bogus
check "an unknown option is refused" rc_is 2
deploy -- --help
check "--help prints the usage" says 'deploy to sweeney@pi0wh'

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
