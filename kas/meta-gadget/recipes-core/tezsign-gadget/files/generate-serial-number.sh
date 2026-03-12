#!/bin/sh
# Generate a stable ASCII USB serial and persist it to /app/tezsign_id

set -eu

readonly APP_ID_FILE="/app/tezsign_id"

sanitize_serial() {
    # Extract only alphanumeric, convert to uppercase, pad to 12 if needed, truncate to 32
    local s
    s=$(printf '%s' "$1" | tr -cd '[:alnum:]' | tr 'a-z' 'A-Z')
    
    # POSIX way to check length
    if [ "${#s}" -lt 12 ]; then
        s=$(printf '%-12s' "$s" | tr ' ' '0')
    fi
    # POSIX way to truncate to 32 chars
    printf '%s' "$s" | cut -c1-32
}

compute_serial_raw() {
    if [ -r /etc/machine-id ]; then
        head -c 32 /etc/machine-id | tr -d '\n'
        return 0
    fi
    if [ -r /sys/firmware/devicetree/base/serial-number ]; then
        tr -d '\0\n' < /sys/firmware/devicetree/base/serial-number | head -c 32
        return 0
    fi
    if grep -qi '^Serial' /proc/cpuinfo 2>/dev/null; then
        awk -F': *' '/^Serial/{s=$2} END{gsub(/^[ \t]+|[ \t]+$/, "", s); print s}' /proc/cpuinfo | head -c 32
        return 0
    fi
    if [ -r /sys/block/mmcblk0/device/cid ]; then
        head -c 32 /sys/block/mmcblk0/device/cid
        return 0
    fi
    # Last resort: random bytes using od
    head -c 16 /dev/urandom | od -An -v -t x1 | tr -d ' \n' | cut -c1-32
}

persist_serial() {
    local serial="$1"
    umask 077
    # Note: /app is already mounted RW by systemd in our setup
    local tmp
    tmp=$(mktemp "${APP_ID_FILE}.XXXXXX")
    printf '%s' "$serial" > "$tmp"
    chmod 400 "$tmp" 2>/dev/null || true
    chown root:root "$tmp" 2>/dev/null || true
    mv -f "$tmp" "$APP_ID_FILE"
}

main() {
    if [ -f "$APP_ID_FILE" ]; then
        return 0
    fi

    local raw
    raw=$(compute_serial_raw || true)
    
    if [ -z "$raw" ]; then
        raw=$(head -c 16 /dev/urandom | od -An -v -t x1 | tr -d ' \n' | cut -c1-32)
    fi
    
    local serial
    serial=$(sanitize_serial "$raw")
    persist_serial "$serial"
    printf '%s\n' "$serial"
}

main "$@"