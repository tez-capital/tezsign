#!/bin/bash

set -e

# This command checks if the package status is NOT 'no such package' (i.e., it exists)
# and then purges only the packages that are found.
sudo apt update

grep -v '^\s*#' /tmp/overlay/packages_to_purge.txt | \
    grep -Fvf <(dpkg-query -W -f='${Package}\n' | sed 's/^/#/') - | \
    xargs sudo apt purge --assume-yes --allow-remove-essential

sudo apt autoremove --assume-yes
sudo apt install initramfs-tools --assume-yes # restore initramfs-tools-bin if it was removed

touch /root/.no_rootfs_resize