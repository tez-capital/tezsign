TEZSIGN_RPI_OVERLAY_BOOT_FILES = ""
TEZSIGN_RPI_OVERLAY_BOOT_FILES:raspberrypi4-tezsign = " \
    overlay_map.dtb;overlays/overlay_map.dtb \
    dwc2.dtbo;overlays/dwc2.dtbo \
"
TEZSIGN_RPI_OVERLAY_BOOT_FILES:append:raspberrypi4-tezsign = "${@' vc4-kms-v3d-pi4.dtbo;overlays/vc4-kms-v3d-pi4.dtbo' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
TEZSIGN_RPI_OVERLAY_BOOT_FILES:raspberrypi0-2w-tezsign = " \
    overlay_map.dtb;overlays/overlay_map.dtb \
    dwc2.dtbo;overlays/dwc2.dtbo \
"
TEZSIGN_RPI_OVERLAY_BOOT_FILES:append:raspberrypi0-2w-tezsign = "${@' vc4-fkms-v3d.dtbo;overlays/vc4-fkms-v3d.dtbo' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
IMAGE_BOOT_FILES:append = " ${TEZSIGN_RPI_OVERLAY_BOOT_FILES}"

do_deploy:append() {
    rm -rf ${DEPLOYDIR}/${BOOTFILES_DIR_NAME}/overlays

    install -m 0644 ${S}/overlays/overlay_map.dtb ${DEPLOYDIR}/overlay_map.dtb
    install -m 0644 ${S}/overlays/dwc2.dtbo ${DEPLOYDIR}/dwc2.dtbo
    install -m 0644 ${S}/overlays/vc4-kms-v3d-pi4.dtbo ${DEPLOYDIR}/vc4-kms-v3d-pi4.dtbo
    install -m 0644 ${S}/overlays/vc4-fkms-v3d.dtbo ${DEPLOYDIR}/vc4-fkms-v3d.dtbo
}
