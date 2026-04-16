SUMMARY = "Tezsign minimal image"
LICENSE = "CLOSED"

inherit core-image

TEZSIGN_COMMON_IMAGE_INSTALL = " \
    base-files \
    base-passwd \
    systemd \
    toybox \
    udev \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'netbase', '', d)} \
    generate-serial-number \
    tezsign-core \
    tezsign-utils \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'dash', '', d)} \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'tezsign-dev', '', d)} \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'dropbear', '', d)} \
    ${CORE_IMAGE_EXTRA_INSTALL} \
"

IMAGE_INSTALL = "${TEZSIGN_COMMON_IMAGE_INSTALL}"

IMAGE_FEATURES = ""
IMAGE_FEATURES += "${@'ssh-server-dropbear' if d.getVar('TEZSIGN_DEV') == '1' else ''}"

IMAGE_FSTYPES = "wic wic.bmap"

# Post-process to move the image
IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"

# Rootfs cleanup — runs for both WIC and initramfs (cpio)
ROOTFS_POSTPROCESS_COMMAND:append = " prune_prod_console_bits; prune_prod_systemd_userland;"

extract_final_image() {
    mkdir -p ${TOPDIR}/../release
    if [ -e ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ]; then
        cp ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ${TOPDIR}/../release/${TEZSIGN_RELEASE_NAME}.img
    fi
}

prune_prod_console_bits() {
    if [ "${TEZSIGN_DEV}" = "1" ]; then
        return
    fi

    rm -f ${IMAGE_ROOTFS}${sysconfdir}/systemd/system/getty.target.wants/getty@tty1.service
}

prune_prod_systemd_userland() {
    if [ "${TEZSIGN_DEV}" = "1" ]; then
        return
    fi

    rm -f \
        ${IMAGE_ROOTFS}${bindir}/bootctl \
        ${IMAGE_ROOTFS}${bindir}/busctl \
        ${IMAGE_ROOTFS}${bindir}/journalctl \
        ${IMAGE_ROOTFS}${bindir}/systemd-ac-power \
        ${IMAGE_ROOTFS}${bindir}/systemd-ask-password \
        ${IMAGE_ROOTFS}${bindir}/systemd-id128 \
        ${IMAGE_ROOTFS}${bindir}/systemd-machine-id-setup \
        ${IMAGE_ROOTFS}${bindir}/systemd-mount \
        ${IMAGE_ROOTFS}${bindir}/systemd-notify \
        ${IMAGE_ROOTFS}${bindir}/systemd-socket-activate \
        ${IMAGE_ROOTFS}${bindir}/systemd-tty-ask-password-agent \
        ${IMAGE_ROOTFS}${bindir}/varlinkctl

    rm -rf ${IMAGE_ROOTFS}${nonarch_libdir}/systemd/catalog
}

do_image_wic[depends] += "app:do_deploy"
do_image_wic[depends] += "linux-mainline:do_deploy"
WKS_FILE = "${THISDIR}/files/storage.wks.in"
WKS_FILE:radxa-zero3-tezsign = "${THISDIR}/files/storage-rockchip.wks.in"

# Rockchip: stage boot files (kernel Image w/ embedded initramfs, DTB, extlinux.conf)
# into a staging directory that the WKS references via --rootfs-dir.
prepare_rockchip_bootfs() {
    if [ "${MACHINE}" != "radxa-zero3-tezsign" ]; then
        return 0
    fi

    BOOTFS="${DEPLOY_DIR_IMAGE}/rockchip-bootfs"
    rm -rf $BOOTFS
    install -d $BOOTFS/extlinux

    # Kernel Image (initramfs-bundled)
    BUNDLED="${DEPLOY_DIR_IMAGE}/${KERNEL_IMAGETYPE}-${INITRAMFS_LINK_NAME}.bin"
    if [ ! -f "$BUNDLED" ]; then
        bbfatal "Initramfs-bundled kernel not found: $BUNDLED"
    fi
    install -m 0644 "$BUNDLED" $BOOTFS/Image

    # Device tree
    install -m 0644 ${DEPLOY_DIR_IMAGE}/rk3566-radxa-zero-3w.dtb $BOOTFS/

    # Generate extlinux.conf
    ARGS="root=/dev/ram0 rw rootfstype=ramfs rdinit=/sbin/init"
    if [ "${TEZSIGN_DEV}" = "1" ]; then
        ARGS="$ARGS earlycon console=tty1 console=ttyS2,1500000n8"
    fi
    cat > $BOOTFS/extlinux/extlinux.conf <<EOF
default TezSign
label TezSign
   kernel /Image
   fdt /rk3566-radxa-zero-3w.dtb
   append $ARGS
EOF
}
do_image_wic[prefuncs] += "prepare_rockchip_bootfs"

# Write Rockchip bootloader binaries to raw sectors (like Armbian does).
# idbloader.img → sector 64, u-boot.itb → sector 16384.
# This must run before extract_final_image copies the .wic to release/.
IMAGE_POSTPROCESS_COMMAND:prepend:radxa-zero3-tezsign = "rockchip_dd_bootloader; "

rockchip_dd_bootloader() {
    IMG="${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic"
    if [ ! -f "$IMG" ]; then
        bbwarn "WIC image not found, skipping bootloader dd"
        return
    fi
    bbnote "Writing idbloader.img to sector 64"
    dd if=${DEPLOY_DIR_IMAGE}/idbloader.img of=$IMG seek=64 conv=notrunc bs=512
    bbnote "Writing u-boot.itb to sector 16384"
    dd if=${DEPLOY_DIR_IMAGE}/u-boot.${UBOOT_SUFFIX} of=$IMG seek=16384 conv=notrunc bs=512
}

