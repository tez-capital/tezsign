SUMMARY = "Tezsign Air-gapped RAM Disk OS"
LICENSE = "MIT"

inherit core-image

INIT_MANAGER = "systemd"
IMAGE_FSTYPES = "cpio.gz"

IMAGE_LINGUAS = ""
IMAGE_NAME_SUFFIX = ""
IMAGE_FEATURES:remove = "package-management"
IMAGE_FEATURES:remove = "doc-pkgs dev-pkgs dbg-pkgs tools-sdk tools-debug tools-profile tools-testapps"

NO_RECOMMENDATIONS = "1"

PACKAGE_EXCLUDE += "kernel-image-* kernel-modules kernel-devicetree"

# This is your actual operating system
IMAGE_INSTALL = " \
    packagegroup-core-boot \
    os-release \
    tezsign-gadget \
"

IMAGE_INSTALL:remove = "kernel-image kernel-modules kernel-devicetree"

# 1. Create the Permanent Environment Mounts and Init Symlink
setup_permanent_ramdisk() {
    # Standard dirs are created by core-image, but explicitly creating yours
    install -d ${IMAGE_ROOTFS}/dev
    install -d ${IMAGE_ROOTFS}/proc
    install -d ${IMAGE_ROOTFS}/sys
    install -d ${IMAGE_ROOTFS}/run
    install -d ${IMAGE_ROOTFS}/tmp

    install -d ${IMAGE_ROOTFS}/app
    install -d ${IMAGE_ROOTFS}/data
    install -d ${IMAGE_ROOTFS}/sys/kernel/config
    install -d ${IMAGE_ROOTFS}/var/log
    
    # Append your specific mount points to fstab
    echo "configfs  /sys/kernel/config  configfs  defaults  0  0" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab
    echo "LABEL=app /app   ext4  ro,exec,noatime,nofail  0   2" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab
    echo "LABEL=data  /data  ext4  rw,noatime,nofail,data=journal   0   2" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab

    rm -rf ${IMAGE_ROOTFS}/usr/lib/systemd/system/initrd*

    # Ensure the kernel can find systemd if booting with rdinit=/init
    ln -sf /usr/lib/systemd/systemd ${IMAGE_ROOTFS}/init
    
    # Remove initrd-release so systemd treats this as the permanent root, not a transient initrd
    rm -f ${IMAGE_ROOTFS}/etc/initrd-release
}

# 2. Dev Password Injector (Dormant in Production)
set_dev_password() {
    # This acts as a safety valve. It ONLY sets the password if the 'dev' user 
    # was actually created by kas-dev.yml. In production, this does nothing!
    if grep -q "^dev:" ${IMAGE_ROOTFS}/etc/passwd; then
        echo "dev:tezsign" | chpasswd -R ${IMAGE_ROOTFS}
    fi
}



# Attach your shell functions to the image generation pipeline
ROOTFS_POSTPROCESS_COMMAND += "setup_permanent_ramdisk; set_dev_password;"