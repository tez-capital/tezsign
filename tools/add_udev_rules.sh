#!/usr/bin/env bash

err() {
  echo "[error] $1" >&2
}

die() {
  err "$1"
  exit 1
}

if [[ $EUID -ne 0 ]]; then
  die "This script must be run as root (e.g. via sudo)."
fi

RULE_PATH="/etc/udev/rules.d/99-tezsign.rules"

cat <<'RULES' > "${RULE_PATH}" || die "Failed to write ${RULE_PATH}."
ACTION=="add|change", SUBSYSTEM=="usb", ATTR{idVendor}=="9997", ATTR{idProduct}=="0001", ATTR{product}=="tezsign-gadget", ATTR{power/control}="on", GROUP="plugdev", MODE="0660"
RULES

if ! udevadm control --reload-rules; then
  die "Failed to reload udev rules."
fi

if ! udevadm trigger; then
  die "Failed to trigger udev reload."
fi

printf 'Installed tezsign host udev rules at %s\n' "${RULE_PATH}"
printf 'Remember to add your user to the plugdev group if needed (sudo usermod -aG plugdev $USER).\n'
