TEZSIGN_RPI_OVERLAY_BOOT_FILES = " \
    overlay_map.dtb;overlays/overlay_map.dtb \
    dwc2.dtbo;overlays/dwc2.dtbo \
    vc4-kms-v3d-pi4.dtbo;overlays/vc4-kms-v3d-pi4.dtbo \
    vc4-fkms-v3d.dtbo;overlays/vc4-fkms-v3d.dtbo \
"
IMAGE_BOOT_FILES:append = " ${TEZSIGN_RPI_OVERLAY_BOOT_FILES}"

do_deploy:append() {
    rm -rf ${DEPLOYDIR}/${BOOTFILES_DIR_NAME}/overlays

    install -m 0644 ${S}/overlays/overlay_map.dtb ${DEPLOYDIR}/overlay_map.dtb
    install -m 0644 ${S}/overlays/dwc2.dtbo ${DEPLOYDIR}/dwc2.dtbo
    install -m 0644 ${S}/overlays/vc4-kms-v3d-pi4.dtbo ${DEPLOYDIR}/vc4-kms-v3d-pi4.dtbo
    install -m 0644 ${S}/overlays/vc4-fkms-v3d.dtbo ${DEPLOYDIR}/vc4-fkms-v3d.dtbo
}
