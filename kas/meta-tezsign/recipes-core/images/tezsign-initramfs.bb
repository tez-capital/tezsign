SUMMARY = "Tezsign initramfs image"
LICENSE = "CLOSED"

require minimal-image.bb

PACKAGE_INSTALL = "${TEZSIGN_COMMON_IMAGE_INSTALL}"
IMAGE_FEATURES = ""
IMAGE_FSTYPES = "${INITRAMFS_FSTYPES}"
IMAGE_NAME_SUFFIX = ""

# The initramfs is the rootfs; do not try to carry separate kernel packages in it.
PACKAGE_EXCLUDE += "kernel-image-* kernel-devicetree"

# This recipe only exists to produce the bundled initramfs payload.
# Clear all post-processing — no WIC, no bootloader dd, no release copy.
IMAGE_POSTPROCESS_COMMAND = ""
IMAGE_POSTPROCESS_COMMAND:radxa-zero3-tezsign = ""

# No-op overrides for WIC-only functions inherited from minimal-image.bb.
rockchip_dd_bootloader() {
    :
}
extract_final_image() {
    :
}
ROOTFS_POSTPROCESS_COMMAND:append = " tezsign_initramfs_make_init_link;"
WKS_FILE = ""

tezsign_initramfs_make_init_link() {
    ln -snf /sbin/init ${IMAGE_ROOTFS}/init
}
