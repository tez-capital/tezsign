# 1. Completely rip out Yocto's switch-root trampoline scripts (Universal)
INITRAMFS_SCRIPTS = ""
PACKAGE_INSTALL:remove = "initramfs-framework-base initramfs-module-udev initramfs-module-fs"

# 2. Ensure systemd is explicitly packed into the RAM disk (Universal)
PACKAGE_INSTALL:append = " systemd"

# 3. Create the Permanent Environment (Universal)
setup_permanent_ramdisk() {
    install -d ${IMAGE_ROOTFS}/dev
    install -d ${IMAGE_ROOTFS}/proc
    install -d ${IMAGE_ROOTFS}/sys
    install -d ${IMAGE_ROOTFS}/run
    install -d ${IMAGE_ROOTFS}/tmp

    install -d ${IMAGE_ROOTFS}/app
    install -d ${IMAGE_ROOTFS}/data
    
    echo "configfs  /sys/kernel/config  configfs  defaults  0  0" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab
    echo "LABEL=app /app   ext4  ro,exec,noatime,nofail  0   2" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab
    echo "LABEL=data  /data  ext4  rw,noatime,nofail,data=journal   0   2" >> ${IMAGE_ROOTFS}${sysconfdir}/fstab

    ln -sf /lib/systemd/systemd ${IMAGE_ROOTFS}/init
    rm -f ${IMAGE_ROOTFS}/etc/initrd-release
}

# 4. Dev Password Injector (Dormant in Production)
set_dev_password() {
    # This acts as a safety valve. It ONLY sets the password if the 'dev' user 
    # was actually created by kas-dev.yml. In production, this does nothing!
    if grep -q "^dev:" ${IMAGE_ROOTFS}/etc/passwd; then
        echo "dev:tezsign" | chpasswd -R ${IMAGE_ROOTFS}
    fi
}

ROOTFS_POSTPROCESS_COMMAND += "setup_permanent_ramdisk; set_dev_password;"