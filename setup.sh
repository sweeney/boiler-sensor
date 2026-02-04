#!/bin/bash
#
# Setup script for boiler-sensor on Raspberry Pi
# Run this from ~/boiler-sensor/ after copying files from dev machine
#

set -e

# Require sudo
if [ "$EUID" -ne 0 ]; then
    echo "Error: This script must be run with sudo"
    echo "Usage: sudo ./setup.sh"
    exit 1
fi

# Get the actual user (not root)
ACTUAL_USER="${SUDO_USER:-$USER}"
INSTALL_DIR="/home/${ACTUAL_USER}/boiler-sensor"

# Verify we're in the right place
if [ ! -f "${INSTALL_DIR}/boiler-sensor-arm" ]; then
    echo "Error: boiler-sensor-arm not found in ${INSTALL_DIR}"
    echo "Make sure you've copied the binary from your dev machine first:"
    echo "  scp boiler-sensor-arm pi@<PI_IP>:~/boiler-sensor/"
    exit 1
fi

if [ ! -f "${INSTALL_DIR}/boiler-sensor.service" ]; then
    echo "Error: boiler-sensor.service not found in ${INSTALL_DIR}"
    echo "Make sure you've copied the service file from your dev machine first:"
    echo "  scp boiler-sensor.service pi@<PI_IP>:~/boiler-sensor/"
    exit 1
fi

echo "Setting up boiler-sensor..."

# Make binary executable
chmod +x "${INSTALL_DIR}/boiler-sensor-arm"
echo "  Made binary executable"

# Create symlinks
ln -sf "${INSTALL_DIR}/boiler-sensor-arm" /usr/local/bin/boiler-sensor
echo "  Created symlink: /usr/local/bin/boiler-sensor"

ln -sf "${INSTALL_DIR}/boiler-sensor.service" /etc/systemd/system/boiler-sensor.service
echo "  Created symlink: /etc/systemd/system/boiler-sensor.service"

# Ensure user is in gpio group
if ! groups "${ACTUAL_USER}" | grep -q gpio; then
    usermod -aG gpio "${ACTUAL_USER}"
    echo "  Added ${ACTUAL_USER} to gpio group (reboot required for this to take effect)"
fi

# Enable and start service
systemctl daemon-reload
systemctl enable boiler-sensor
systemctl start boiler-sensor
echo "  Enabled and started boiler-sensor service"

echo ""
echo "Setup complete! Check status with:"
echo "  sudo systemctl status boiler-sensor"
echo "  sudo journalctl -u boiler-sensor -f"
