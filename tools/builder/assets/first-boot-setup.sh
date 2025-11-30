#!/bin/bash

# ==============================================================================
# Armbian First-Boot Provisioning Script for Air-Gapped Devices
# ==============================================================================

readonly TEZSIGN_ID="1000"
readonly REGISTRAR_ID="1001"

readonly SCRIPT_LOG_FILE="/var/log/first-boot-setup.log"
readonly SCRIPT_LOCK_FILE="/etc/first-boot-setup.lock"

# Exit if already ran
if [ -f "${SCRIPT_LOCK_FILE}" ]; then
    echo "First-boot setup has already been completed. Exiting."
    exit 0
fi

# Log everything
exec > "${SCRIPT_LOG_FILE}" 2>&1
echo "--- Starting First-Boot Provisioning Script ---"
date

### 1) Disable networking daemons (you can re-enable later as needed)
echo "[*] Disabling networking services..."
systemctl disable --now systemd-networkd-wait-online.service
systemctl disable --now bluetooth.service
systemctl disable --now wpa_supplicant.service
systemctl disable --now NetworkManager.service
systemctl disable --now systemd-networkd.service
systemctl disable --now systemd-resolved.service
systemctl disable --now systemd-timesyncd.service
# ssh
systemctl disable --now ssh.service
sed -i 's/^#*PermitRootLogin .*/PermitRootLogin no/' /etc/ssh/sshd_config # good practice even if ssh is disabled

if command -v rfkill >/dev/null 2>&1; then rfkill block all; fi
for iface in $(ls /sys/class/net | grep -v lo); do
    ip link set "$iface" down
done
echo "[+] Networking disabled."

# Create group dev_manager
if ! getent group dev_manager >/dev/null 2>&1; then
    groupadd dev_manager
    echo "[+] Group 'dev_manager' created."
else
    echo "[*] Group 'dev_manager' already exists."
fi

# Create standard user
if ! id -u "tezsign" >/dev/null 2>&1; then
    useradd -r -u "${TEZSIGN_ID}" -s /usr/sbin/nologin "tezsign"
    usermod -aG dev_manager tezsign
fi
if ! id -u "registrar" >/dev/null 2>&1; then
    useradd -r -u "${REGISTRAR_ID}" -s /usr/sbin/nologin "registrar"
    usermod -aG dev_manager registrar
fi
chown registrar:registrar /usr/local/bin/ffs_registrar # claim registrar binary for registrar user

echo "[+] Users configured."

### 4) Device identity / hostname / serial
hostnamectl set-hostname tezsign
echo "127.0.1.1 $(cat /etc/hostname)" >> /etc/hosts # ensure hostname resolves locally

# Generate/persist device serial (goes to /app/tezsign_id)
/usr/local/bin/generate-serial-number.sh >/dev/null

### 5) Prepare read-only mounts for next boot
echo "[*] Configuring root/boot as read-only for next boot..."
# add 'ro,' to root and /boot lines in fstab (idempotent)
sed -i -E 's|^(\S+\s+/\s+\w+\s+)([^,]*)(.*)|\1ro,\2\3|' /etc/fstab
sed -i -E 's|^(\S+\s+/boot\s+\w+\s+)([^,]*)(.*)|\1ro,\2\3|' /etc/fstab
sed -i -E 's|^(\S+\s+/boot/firmware\s+\w+\s+)([^,]*)(.*)|\1ro,\2\3|' /etc/fstab
echo "[+] /etc/fstab updated."

### 6) Lock down
chmod u-s /usr/bin/sudo
echo "Setuid bit removed from /usr/bin/sudo. Privilege escalation via sudo is disabled."

cut -d: -f1 /etc/passwd | xargs -n1 passwd -l
echo "All user accounts have been locked. Login is disabled."

# Enable dev mode if applicable
if command -v /usr/local/bin/enable-dev.sh >/dev/null 2>&1; then
    /usr/local/bin/enable-dev.sh
fi

### 7) Finalize
touch "${SCRIPT_LOCK_FILE}"
systemctl disable first-boot-setup.service
rm -f /etc/systemd/system/first-boot-setup.service

echo "--- First-Boot Provisioning Complete. Rebooting now! ---"
reboot
