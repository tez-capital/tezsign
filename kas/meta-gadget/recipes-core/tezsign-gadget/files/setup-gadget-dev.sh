#!/bin/sh
set -eu

GADGET_DIR="/sys/kernel/config/usb_gadget/g1"
MAC_ADDR="ae:d3:e6:cd:ff:f2"
HOST_MAC_ADDR="ae:d3:e6:cd:ff:f3"

echo "Adding ECM function to gadget..."

# 1. Create the ECM function directory
ECM_FUNC_DIR="${GADGET_DIR}/functions/ecm.usb0"
mkdir -p "${ECM_FUNC_DIR}"

echo "${MAC_ADDR}" > "${ECM_FUNC_DIR}/dev_addr"
echo "${HOST_MAC_ADDR}" > "${ECM_FUNC_DIR}/host_addr"

# 2. Link the new ECM function to the existing configuration
CONF_DIR="${GADGET_DIR}/configs/c.1"
ln -s "${ECM_FUNC_DIR}" "${CONF_DIR}"

echo "ECM function linked successfully."