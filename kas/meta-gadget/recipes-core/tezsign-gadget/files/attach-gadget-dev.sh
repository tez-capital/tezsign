#!/bin/sh
set -eu

IP_ADDRESS="10.10.10.1/24"
INTERFACE="usb0"

if [ ! -e "/sys/class/net/${INTERFACE}" ]; then
  echo "Interface ${INTERFACE} did not appear yet."
  exit 1
fi

# If already configured, skip (idempotent)
if ! ip addr show "${INTERFACE}" | grep -q "${IP_ADDRESS}"; then
  echo "Configuring ${INTERFACE} with ${IP_ADDRESS}..."
  ip addr add "${IP_ADDRESS}" dev "${INTERFACE}"
fi

ip link set "${INTERFACE}" up
echo "Dev network interface ${INTERFACE} is up."