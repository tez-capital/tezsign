#!/bin/sh
set -e

# Load the module (No sudo required)
modprobe libcomposite

readonly VID="0x9997"
readonly PID="0x0001"
readonly APP_ID_FILE="/app/tezsign_id"
readonly LEGACY_APP_ID_FILE="/data/tezsign_id"

# POSIX-compliant file checks
if [ -f "${APP_ID_FILE}" ]; then
  SERIAL="$(cat "${APP_ID_FILE}" 2>/dev/null)"
elif [ -f "${LEGACY_APP_ID_FILE}" ]; then
  SERIAL="$(cat "${LEGACY_APP_ID_FILE}" 2>/dev/null)"
else
  SERIAL=""
fi

readonly MANUFACTURER="TzC"
readonly PRODUCT="tezsign-gadget"

# 1. Create the gadget directory
GADGET_DIR="/sys/kernel/config/usb_gadget/g1"
mkdir -p "${GADGET_DIR}"

# 2. Set the Vendor and Product IDs
echo "${VID}" > "${GADGET_DIR}/idVendor"
echo "${PID}" > "${GADGET_DIR}/idProduct"

# 3. Create language-specific directory for strings
mkdir -p "${GADGET_DIR}/strings/0x409"
echo "${SERIAL}" > "${GADGET_DIR}/strings/0x409/serialnumber"
echo "${MANUFACTURER}" > "${GADGET_DIR}/strings/0x409/manufacturer"
echo "${PRODUCT}" > "${GADGET_DIR}/strings/0x409/product"

# 4. Create the FunctionFS function
FFS_FUNC_DIR="${GADGET_DIR}/functions/ffs.tezsign"
mkdir -p "${FFS_FUNC_DIR}"

# 5. Create a configuration and link the function to it
CONF_DIR="${GADGET_DIR}/configs/c.1"
mkdir -p "${CONF_DIR}"
ln -s "${FFS_FUNC_DIR}" "${CONF_DIR}"

mkdir -p /dev/ffs/tezsign
mount -t functionfs tezsign /dev/ffs/tezsign

# Set ownership and permissions
chmod 770 /dev/ffs/tezsign
chown :dev_manager /dev/ffs/tezsign
chown registrar:registrar /dev/ffs/tezsign/ep0