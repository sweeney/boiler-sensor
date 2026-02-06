#!/usr/bin/env bash
set -euo pipefail

# Usage: ./deploy.sh [user@host]
PI_HOST="${1:-sweeney@pi0wh}"
PI_DIR="boiler-sensor"
SERVICE="boiler-sensor"
KEEP_VERSIONS=3

VERSION=$(date +%Y%m%d-%H%M%S)
REMOTE_BIN="boiler-sensor-${VERSION}"

echo "==> Building for Pi Zero (linux/arm6)..."
GOOS=linux GOARCH=arm GOARM=6 go build -o boiler-sensor-arm ./cmd/boiler-sensor/

echo "==> Uploading to ${PI_HOST}..."
ssh "${PI_HOST}" "mkdir -p ~/${PI_DIR}"
scp -q boiler-sensor-arm "${PI_HOST}:~/${PI_DIR}/${REMOTE_BIN}"
scp -q boiler-sensor.service "${PI_HOST}:~/${PI_DIR}/boiler-sensor.service"

echo "==> Installing ${REMOTE_BIN}..."
ssh "${PI_HOST}" "\
  chmod +x ~/${PI_DIR}/${REMOTE_BIN} && \
  ln -sfn ${REMOTE_BIN} ~/${PI_DIR}/boiler-sensor-arm"

# First-time setup if symlinks don't exist yet
NEEDS_SETUP=$(ssh "${PI_HOST}" "\
  [ -L /usr/local/bin/boiler-sensor ] && echo no || echo yes")

if [ "${NEEDS_SETUP}" = "yes" ]; then
  echo "==> First-time setup..."
  PI_HOME=$(ssh "${PI_HOST}" "echo \$HOME")
  ssh "${PI_HOST}" "\
    sudo ln -sf ${PI_HOME}/${PI_DIR}/boiler-sensor-arm /usr/local/bin/boiler-sensor && \
    sudo ln -sf ${PI_HOME}/${PI_DIR}/boiler-sensor.service /etc/systemd/system/boiler-sensor.service && \
    (groups | grep -q gpio || sudo usermod -aG gpio \$(whoami)) && \
    sudo systemctl daemon-reload && \
    sudo systemctl enable ${SERVICE}"
fi

echo "==> Restarting service..."
ssh "${PI_HOST}" "sudo systemctl daemon-reload && sudo systemctl restart ${SERVICE}"

echo "==> Cleaning old versions (keeping ${KEEP_VERSIONS})..."
ssh "${PI_HOST}" "\
  cd ~/${PI_DIR} && \
  ls -t boiler-sensor-* \
    | grep -v boiler-sensor-arm \
    | grep -v boiler-sensor.service \
    | tail -n +$((KEEP_VERSIONS + 1)) \
    | xargs -r rm --"

echo "==> Deployed ${VERSION}:"
ssh "${PI_HOST}" "sudo journalctl -u ${SERVICE} -n 3 --no-pager"
