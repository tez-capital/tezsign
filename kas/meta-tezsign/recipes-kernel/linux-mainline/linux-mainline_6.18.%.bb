DESCRIPTION = "Mainline Linux kernel"
LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/GPL-2.0-only;md5=801f80980d171dd6425610833a22dbe6"

# Inheriting kernel-yocto enables Yocto's native configuration fragment merging
inherit kernel kernel-yocto

PROVIDES = "virtual/kernel"

KBUILD_DEFCONFIG = ""
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git;protocol=https;branch=linux-6.18.y"
SRCREV = "${AUTOREV}"

LINUX_VERSION = "6.18"
PV = "${LINUX_VERSION}+git${SRCPV}"

# Skip the strict version verification so ${AUTOREV} can dynamically track 6.18.x point updates
KERNEL_VERSION_SANITY_SKIP = "1"

# Set the native Kconfig baseline. This strips out all kernel defaults so 
# ONLY the options explicitly defined in your defconfig and .cfg fragments are built.
KCONFIG_MODE = "--allnoconfig"

# Global files and patches
SRC_URI:append = " \
    file://defconfig \
    file://0001-dwc2-gadget-skip-stop-xfr-on-active-dequeue.patch \
    file://0002-arm64-dts-rockchip-radxa-zero-3w-usb-peripheral.patch \
    file://tezsign-common.cfg \
    ${@'file://tezsign-common-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''} \
"

# Raspberry Pi Zero 2W specific fragments
SRC_URI:append:raspberrypi0-2w-tezsign = " \
    file://rpi-common.cfg \
    file://rpi-zero2w.cfg \
    ${@'file://rpi-dev-common.cfg file://rpi-zero2w-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''} \
"

# Raspberry Pi 4 specific fragments
SRC_URI:append:raspberrypi4-tezsign = " \
    file://rpi-common.cfg \
    file://rpi4.cfg \
    ${@'file://rpi-dev-common.cfg file://rpi4-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''} \
"

# Radxa Zero 3 specific fragments
SRC_URI:append:radxa-zero3-tezsign = " \
    file://radxa-common.cfg \
    file://radxa-zero3.cfg \
    ${@'file://radxa-dev-common.cfg file://radxa-zero3-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''} \
"