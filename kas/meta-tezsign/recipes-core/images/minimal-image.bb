SUMMARY = "Tezsign minimal image"
LICENSE = "CLOSED"

inherit core-image

IMAGE_INSTALL = " \
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

IMAGE_FEATURES = ""
IMAGE_FEATURES += "${@'ssh-server-dropbear' if d.getVar('TEZSIGN_DEV') == '1' else ''}"

IMAGE_FSTYPES = "wic wic.bmap"

# Post-process to move the image
IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"
IMAGE_POSTPROCESS_COMMAND += "prune_prod_console_bits;"

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

do_image_wic[depends] += "app:do_deploy"
WKS_FILE = "${THISDIR}/files/storage.wks.in"
