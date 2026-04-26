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
ROOTFS_POSTPROCESS_COMMAND:append = " enable_dev_local_getty; tezsign_initramfs_make_init_link; tezsign_write_fstab;"
WKS_FILE = ""

tezsign_initramfs_make_init_link() {
    ln -snf /sbin/init ${IMAGE_ROOTFS}/init
}

tezsign_write_fstab() {
    cat > ${IMAGE_ROOTFS}${sysconfdir}/fstab <<'EOF'
# <dev>                    <mount>  <type>  <options>                                                           <dump> <fsck>
LABEL=app                  /app     ext4    ro,exec,noatime,data=writeback                                        0      1
LABEL=data                 /data    ext4    rw,noatime,nodiratime,data=writeback,barrier=1,commit=15,errors=remount-ro 0  1
EOF
    install -d ${IMAGE_ROOTFS}/app
    install -d ${IMAGE_ROOTFS}/data
}
