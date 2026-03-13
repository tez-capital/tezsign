SUMMARY = "Tezsign Gadget Flashable SD Card Image"
LICENSE = "MIT"

inherit image

# 1. Strip the dummy root filesystem completely empty
IMAGE_INSTALL = ""
IMAGE_LINGUAS = ""
IMAGE_FEATURES = ""
PACKAGE_INSTALL = ""

# 2. Assign the WIC configuration
IMAGE_FSTYPES = "wic wic.bmap"
WKS_FILE = "gadget.wks"

# 3. Tell WIC to wait for the real OS to finish compiling
do_image_wic[depends] += "virtual/kernel:do_deploy core-image-minimal-initramfs:do_image_complete"

# 4. Integrate your beautiful custom rename script directly into the recipe!
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