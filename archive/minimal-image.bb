SUMMARY = "Tezsign minimal image"
LICENSE = "MIT"

inherit core-image

# packagegroup-core-boot handles systemd/busybox automatically 
# based on your DISTRO settings.
IMAGE_INSTALL = " \
    packagegroup-core-boot \
    kernel-modules \
    dash \
    ${CORE_IMAGE_EXTRA_INSTALL} \
"

# Keep features empty for a true minimal build
IMAGE_FEATURES = ""

IMAGE_FSTYPES = "wic wic.bmap"

# Post-process to move the image
IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"

extract_final_image() {
    mkdir -p ${TOPDIR}/../release
    if [ -e ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ]; then
        cp ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ${TOPDIR}/../release/tezsign.img
    fi
}