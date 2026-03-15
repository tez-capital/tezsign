SUMMARY = "Tezsign Gadget Flashable SD Card Image"
LICENSE = "MIT"

inherit image

# Strip the dummy root filesystem completely empty
IMAGE_INSTALL = ""
IMAGE_LINGUAS = ""
IMAGE_FEATURES = ""
PACKAGE_INSTALL = ""

# Assign the WIC configuration
IMAGE_FSTYPES = "wic wic.bmap"
WKS_FILE = "gadget.wks"

# Tell WIC to wait for the real RAM OS to finish compiling
do_image_wic[depends] += "virtual/kernel:do_deploy tezsign-ramdisk:do_image_complete"

# Integrate your custom rename script directly into the recipe
FINAL_IMAGE_NAME ?= "gadget-os.img"

IMAGE_POSTPROCESS_COMMAND += "extract_final_image;"

extract_final_image() {
    mkdir -p ${TOPDIR}/../release
    if [ -e ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ]; then
        cp ${IMGDEPLOYDIR}/${IMAGE_LINK_NAME}.wic ${TOPDIR}/../release/${FINAL_IMAGE_NAME}
        echo "=================================================================="
        echo "SUCCESS: Your final flashable image is at release/${FINAL_IMAGE_NAME}"
        echo "=================================================================="
    fi
}