SUMMARY = "Tezsign minimal image"
LICENSE = "CLOSED"

inherit core-image

IMAGE_INSTALL = " \
    packagegroup-core-boot \
    kernel-modules \
    dash \
    generate-serial-number \
    tezsign-core \
    tezsign-utils \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'tezsign-dev', '', d)} \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', 'dropbear', '', d)} \
    ${CORE_IMAGE_EXTRA_INSTALL} \
"

IMAGE_FEATURES = ""
IMAGE_FEATURES += "${@'ssh-server-dropbear' if d.getVar('TEZSIGN_DEV') == '1' else ''}"

IMAGE_FSTYPES = "wic wic.bmap"

# Post-process to move the image
IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"

extract_final_image() {
    mkdir -p ${TOPDIR}/../release
    if [ -e ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ]; then
        cp ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ${TOPDIR}/../release/${TEZSIGN_RELEASE_NAME}.img
    fi
}

do_image_wic[depends] += "app:do_deploy"
WKS_FILE = "${THISDIR}/files/storage.wks.in"