#!/usr/bin/env bash
# Generate a stable ASCII USB serial (12–32 chars, [A-Za-z0-9], uppercased),
# then ALWAYS persist it to /app/tezsign_id

set -eu

readonly APP_ID_FILE="/app/tezsign_id"

sanitize_serial() {
  local s="$1"
  s="$(printf '%s' "$s" | tr -cd '[:alnum:]' | tr '[:lower:]' '[:upper:]')"
  if (( ${#s} < 12 )); then
    s="$(printf '%-12s' "$s" | tr ' ' '0')"
  fi
  printf '%s' "${s:0:32}"
}

compute_serial_raw() {
  # 1) machine-id (most stable)
  if [[ -r /etc/machine-id ]]; then
    head -c 32 /etc/machine-id | tr -d '\n'
    return
  fi
  # 2) device-tree serial-number (many ARM boards)
  if [[ -r /sys/firmware/devicetree/base/serial-number ]]; then
    tr -d '\0\n' < /sys/firmware/devicetree/base/serial-number | head -c 32
    return
  fi
  # 3) /proc/cpuinfo Serial (Raspberry Pi, some ARM SoCs)
  if grep -qi '^Serial' /proc/cpuinfo 2>/dev/null; then
    awk -F': *' '/^Serial/{s=$2} END{gsub(/^[ \t]+|[ \t]+$/, "", s); print s}' /proc/cpuinfo | head -c 32
    return
  fi
  # 4) NIC MACs → SHA256 → 32 hex chars
  if command -v ip >/dev/null 2>&1; then
    # Extract all MACs in one awk, strip colons, concatenate, no newline
    macs="$(ip -o link show 2>/dev/null | awk '{
      if (match($0, /link\/ether ([0-9a-f:]+)/, m)) {
        gsub(":", "", m[1]); printf "%s", m[1];
      }
    }')"
    if [[ -n "${macs}" ]]; then
      printf '%s' "$macs" | sha256sum | awk '{print substr($1,1,32)}'
      return
    fi
  fi
  # 5) eMMC CID (common on SBCs)
  if [[ -r /sys/block/mmcblk0/device/cid ]]; then
    head -c 32 /sys/block/mmcblk0/device/cid
    return
  fi
  # 6) last resort: random 16 bytes → 32 hex chars
  head -c 16 /dev/urandom | xxd -p -c 32
}

persist_serial() {
  local serial="$1"
  umask 077
  local tmp
  tmp="$(mktemp "${APP_ID_FILE}.XXXXXX")"
  printf '%s' "$serial" > "$tmp"
  chmod 400 "$tmp" 2>/dev/null || true
  chown root:root "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$APP_ID_FILE"
}

main() {
  if [[ -f "$APP_ID_FILE" ]]; then
    return # already exists
  fi

  raw="$(compute_serial_raw || true)"
  [[ -z "${raw:-}" ]] && raw="$(head -c 16 /dev/urandom | xxd -p -c 32)"
  serial="$(sanitize_serial "$raw")"
  mount -o remount,rw /app
  persist_serial "$serial"
  printf '%s\n' "$serial"
}

main "$@"
