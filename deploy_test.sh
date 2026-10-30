#!/usr/bin/env bash
# Tests for deploy.sh. Each case runs the real script in a throwaway git repo
# against stubs: ssh runs the "remote" commands locally with HOME pointed at a
# fake Pi home, and curl, mosquitto_sub, rsync, go, systemctl, journalctl and
# sudo are scripted from that fake Pi's state. No network, no Pi.
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
cd "$FAKE_HOME"
HOME="$FAKE_HOME" PATH="$RSTUBS:$PATH" exec bash -c "$*"
EOF

cat >"$STUBS/rsync" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_RSYNC_FAIL:-0}" = 1 ] && exit 12
src="${*: -2:1}" dest="${*: -1}"
cp "$src" "$FAKE_HOME/${dest#*:}"
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
        { echo "FAKEBIN version=$ver"; head -c 1100000 /dev/zero; } >"$out" ;;
esac
EOF

# The Pi's HTTP server, answering for whichever build systemctl last started.
cat >"$STUBS/curl" <<'EOF'
#!/usr/bin/env bash
[ "${FAKE_HTTP_DOWN:-0}" = 1 ] && exit 7
url="${*: -1}" path="/${url#http://*/}"
running=$(cat "$FAKE_STATE/running")
ver=$(head -1 "$FAKE_HOME/boiler-sensor/$running" | sed -n 's/^FAKEBIN version=//p')
started=$(cat "$FAKE_STATE/started")
if [ "${FAKE_CRASHLOOP:-0}" = 1 ] && [ -n "$ver" ]; then
    n=$(($(cat "$FAKE_STATE/calls" 2>/dev/null || echo 0) + 1)); echo $n >"$FAKE_STATE/calls"
    started="2026-10-30T10:00:0${n: -1}Z"
fi
ch=$(cat "$FAKE_STATE/ch") hw=$(cat "$FAKE_STATE/hw")
case "$path" in
    /index.json)
        printf '{"status":{"ch":"%s","hw":"%s","ready":true,"uptime_seconds":5,"start_time":"%s","mqtt":{"connected":true,"broker":"tcp://127.0.0.1:1883"}}}\n' "$ch" "$hw" "$started" ;;
    /healthz)
        [ -n "$ver" ] || exit 22 # a pre-/healthz build: 404
        status=ok
        [ "${FAKE_NEW_BROKEN:-0}" = 1 ] && status=starting
        printf '{"status":"%s","version":"%s","start_time":"%s","ready":true,"mqtt_connected":true,"channels":{"ch":"%s","hw":"%s"}}\n' \
            "$status" "$ver" "$started" "$ch" "$hw" ;;
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
exec "$@"
EOF

cat >"$RSTUBS/systemctl" <<'EOF'
#!/usr/bin/env bash
echo "systemctl $*" >>"$FAKE_STATE/log"
case "$1" in
    is-active) echo active ;;
    restart)
        basename "$(readlink "$HOME/boiler-sensor/boiler-sensor-arm")" >"$FAKE_STATE/running"
        n=$(wc -l <"$FAKE_STATE/log" | tr -d ' ')
        printf '2026-10-30T10:%02d:00Z\n' "$n" >"$FAKE_STATE/started" ;;
esac
EOF

cat >"$RSTUBS/journalctl" <<'EOF'
#!/usr/bin/env bash
echo "boiler-sensor $(cat "$FAKE_STATE/running")"
[ "${FAKE_PANIC:-0}" = 1 ] && echo "panic: runtime error: index out of range"
true
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
    mkdir -p "$WORK/case"/{home/boiler-sensor,state,bin}
    FAKE_HOME="$WORK/case/home" FAKE_STATE="$WORK/case/state"
    local pi="$FAKE_HOME/boiler-sensor"
    for b in boiler-sensor-20260309-115805 boiler-sensor-20260320-093441 "$LEGACY"; do
        echo LEGACY >"$pi/$b"
        sleep 0.01 # distinct mtimes for ls -t
    done
    ln -s "$LEGACY" "$pi/boiler-sensor-arm"
    cp "$HERE/boiler-sensor.service" "$pi/boiler-sensor.service"
    ln -s "$pi/boiler-sensor-arm" "$WORK/case/bin/boiler-sensor"
    echo "$LEGACY" >"$FAKE_STATE/running"
    echo 2026-07-07T06:14:53Z >"$FAKE_STATE/started"
    echo OFF >"$FAKE_STATE/ch"
    echo OFF >"$FAKE_STATE/hw"
    : >"$FAKE_STATE/log"

    REPO="$WORK/case/repo"
    git init -q --bare "$WORK/case/origin.git"
    git init -q -b main "$REPO"
    cp "$HERE/deploy.sh" "$HERE/boiler-sensor.service" "$HERE/.gitignore" "$REPO/"
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

pass=0 fail=0
OUT="" RC=0

# deploy [VAR=value...] -- [deploy.sh args...]; stdin is passed through.
deploy() {
    local envs=()
    while [ $# -gt 0 ] && [ "$1" != "--" ]; do envs+=("$1"); shift; done
    [ $# -gt 0 ] && shift
    OUT=$(env PATH="$STUBS:$PATH" RSTUBS="$RSTUBS" FAKE_HOME="$FAKE_HOME" FAKE_STATE="$FAKE_STATE" \
        DEPLOY_BIN_LINK="$WORK/case/bin/boiler-sensor" DEPLOY_HTTP_HOST=pi.test \
        DEPLOY_VERIFY_TIMEOUT=3 DEPLOY_SETTLE=0 ${envs[@]+"${envs[@]}"} \
        "$REPO/deploy.sh" "$@" 2>&1) && RC=0 || RC=$?
}

# count GLOB: how many entries in the fake Pi's ~/boiler-sensor match.
count() { find "$FAKE_HOME/boiler-sensor" -mindepth 1 -maxdepth 1 -name "$1" | wc -l | tr -d ' '; }
link() { readlink "$FAKE_HOME/boiler-sensor/boiler-sensor-arm"; }
restarts() { grep -c 'systemctl restart' "$FAKE_STATE/log" || true; }

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
deploy FAKE_CRASHLOOP=1 -- --yes
check "a crash-looping build is rolled back" link_is "$LEGACY"
check "it says it did not stay up" says 'did not stay up'

fresh
deploy FAKE_PANIC=1 -- --yes
check "a panic in the journal triggers rollback" link_is "$LEGACY"
check "it quotes the panic" says 'panic: runtime error'

fresh
deploy FAKE_STALE_HB=1 -- --yes
check "a stale broker heartbeat triggers rollback" link_is "$LEGACY"
check "it explains the broker check" says 'not a HEARTBEAT from this run'

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
check "--check only warns about them" says 'a deploy would refuse'

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
check "a corrupted upload is refused" says 'upload corrupted'
check "and nothing is switched" link_is "$LEGACY"
check "and the staged file is removed" [ "$(count '.incoming-*')" -eq 0 ]

fresh
OUT=$(echo n | env PATH="$STUBS:$PATH" RSTUBS="$RSTUBS" FAKE_HOME="$FAKE_HOME" FAKE_STATE="$FAKE_STATE" \
    DEPLOY_BIN_LINK="$WORK/case/bin/boiler-sensor" DEPLOY_HTTP_HOST=pi.test "$REPO/deploy.sh" 2>&1) && RC=0 || RC=$?
check "answering no at the prompt declines" rc_is 2
check "a declined deploy uploads and changes nothing" [ "$(count '*')" -eq 5 ]

fresh
echo "# changed" >>"$REPO/boiler-sensor.service"
git -C "$REPO" -c user.name=t -c user.email=t@t commit -qam unit
git -C "$REPO" push -q origin main
VERSION=$(git -C "$REPO" rev-parse --short=7 HEAD)
deploy -- --yes
check "a changed unit file is installed" says 'unit file changed'
check "and systemd reloaded" [ "$(grep -c daemon-reload "$FAKE_STATE/log")" -eq 1 ]

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

fresh
deploy -- --bogus
check "an unknown option is refused" rc_is 2
deploy -- --help
check "--help prints the usage" says 'deploy to sweeney@pi0wh'

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
