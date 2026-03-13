#!/bin/sh
# Generate a stable ASCII USB serial and persist it to /app/tezsign_id

set -eu

readonly APP_ID_FILE="/app/tezsign_id"

exec > /dev/console 2>&1 

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
        # Use tr to drop newlines, cut for length
        tr -d '\n' < /etc/machine-id | cut -c1-32
        return 0
    fi
    if [ -r /sys/firmware/devicetree/base/serial-number ]; then
        tr -d '\0\n' < /sys/firmware/devicetree/base/serial-number | cut -c1-32
        return 0
    fi
    if grep -qi '^Serial' /proc/cpuinfo 2>/dev/null; then
        awk -F': *' '/^Serial/{s=$2} END{gsub(/^[ \t]+|[ \t]+$/, "", s); print s}' /proc/cpuinfo | cut -c1-32
        return 0
    fi
    if [ -r /sys/block/mmcblk0/device/cid ]; then
        cut -c1-32 /sys/block/mmcblk0/device/cid
        return 0
    fi
    
    dd if=/dev/urandom bs=1 count=16 2>/dev/null | od -An -v -t x1 | tr -d ' \n' | cut -c1-32
}

persist_serial() {
    local serial="$1"
    umask 077
    # Note: /app is already mounted RW by systemd in our setup
    local tmp="/tmp/tezsign_id.tmp.$$"
    printf '%s' "$serial" > "$tmp"
    chmod 400 "$tmp" 2>/dev/null || true
    chown root:root "$tmp" 2>/dev/null || true

    mount -o remount,rw /app
    mv -f "$tmp" "$APP_ID_FILE"
    mount -o remount,ro /app
}

main() {
    if [ -f "$APP_ID_FILE" ]; then
        return 0
    fi

    local raw
    raw=$(compute_serial_raw || true)
    
    if [ -z "$raw" ]; then
        # Also fix the failsafe random generator here!
        raw=$(dd if=/dev/urandom bs=1 count=16 2>/dev/null | od -An -v -t x1 | tr -d ' \n' | cut -c1-32)
    fi
    
    local serial
    serial=$(sanitize_serial "$raw")
    persist_serial "$serial"
    printf '%s\n' "$serial"
}

# --- DIAGNOSTIC BLOCK ---
echo "========================================"
echo "          FILESYSTEM DIAGNOSTICS        "
echo "========================================"

echo ">>> 1. CURRENT FSTAB:"
cat /etc/fstab

echo -e "\n>>> 2. KERNEL PARTITIONS:"
cat /proc/partitions

echo -e "\n>>> 3. BLKID (Checking for 'app' and 'data' labels):"
blkid || echo "blkid command failed"

echo -e "\n>>> 4. UDEV SYMLINKS:"
ls -l /dev/disk/by-label/ || echo "WARNING: udev has not generated label symlinks!"

echo -e "\n>>> 5. MANUAL MOUNT ATTEMPT:"
# If systemd failed to mount it, let's try to do it manually with maximum verbosity
mount -v -t ext4 LABEL=app /app || {
    echo "MANUAL MOUNT FAILED! Dumping recent kernel logs:"
    dmesg | tail -n 15
}

echo "========================================"
sleep 10 # Pause for a moment so you can read it if it scrolls fast!

main "$@"