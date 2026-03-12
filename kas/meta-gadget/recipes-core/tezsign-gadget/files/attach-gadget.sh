#!/bin/sh
# /usr/bin/attach-gadget.sh
set -eu

GADGET_DIR="/sys/kernel/config/usb_gadget/g1"

# Find a UDC that actually exists
UDC="$(ls /sys/class/udc 2>/dev/null | head -n1 || true)"

if [ -z "${UDC}" ]; then
  echo "No UDC available; cannot attach gadget now."
  exit 1
fi

if grep -q "^${UDC}\$" "${GADGET_DIR}/UDC" 2>/dev/null; then
  echo "UDC is already set to ${UDC}."
  exit 0
fi

echo "${UDC}" > "${GADGET_DIR}/UDC"
echo "Attached gadget to UDC: ${UDC}"

# Create link to soft_connect
ln -sf "/sys/class/udc/${UDC}/soft_connect" "/tmp/soft_connect"
chown registrar:registrar "/tmp/soft_connect" # allow ffs_registrar to access it

# Wait for endpoints and fix ownership
# (These endpoints are created by the kernel only AFTER your user-space daemon writes to ep0)
for ep in /dev/ffs/tezsign/ep1 /dev/ffs/tezsign/ep2 /dev/ffs/tezsign/ep3 /dev/ffs/tezsign/ep4; do
  while [ ! -e "$ep" ]; do 
    sleep 1
  done
  chown tezsign:tezsign "$ep"
  echo "Set ownership for $ep"
done

echo "All endpoints secured."