KVER := "${PV}"
require linux-mainline.inc
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git;branch=master;protocol=https"
SRCREV = "0ff41df1cb268fc69e703a08a57ee14ae967d0ca"
SRC_URI:append:raspberrypi5-mainline = " file://defconfig"

PV = "6.18"


SUMMARY = "Mainline Linux Kernel for Raspberry Pi Zero 2W"
LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://COPYING;md5=6bc538ed5bd9a7fc9398086aedcd7e46"

DEPENDS += "openssl-native util-linux-native"

inherit kernel

PV = "6.18"
LINUX_VERSION = "6.18"

SRC_URI = " \
    git://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git;protocol=https;branch=linux-6.18.y \
    file://stripped.cfg \
"

# TODO: Pin to a specific stable release commit for reproducible builds
# Find the latest tag: git ls-remote --tags https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git 'v6.18.*'
SRCREV ?= "${AUTOREV}"

S = "${WORKDIR}/git"

COMPATIBLE_MACHINE = "raspberrypi0-2w-64"

# Mainline uses bcm2837 naming (not bcm2710 like the RPi Foundation kernel)
KERNEL_DEVICETREE = "broadcom/bcm2837-rpi-zero-2-w.dtb"

# Start from the standard arm64 defconfig, then apply our stripped.cfg fragment
KBUILD_DEFCONFIG = "defconfig"

do_configure:append() {
    ${S}/scripts/kconfig/merge_config.sh -m -O ${B} ${B}/.config ${WORKDIR}/stripped.cfg
    oe_runmake -C ${S} O=${B} olddefconfig
}

do_configure:prepend() {
    echo "" >> ${S}/arch/arm64/boot/dts/broadcom/bcm2837-rpi-zero-2-w.dts
    echo "&usb {" >> ${S}/arch/arm64/boot/dts/broadcom/bcm2837-rpi-zero-2-w.dts
    echo "    dr_mode = \"peripheral\";" >> ${S}/arch/arm64/boot/dts/broadcom/bcm2837-rpi-zero-2-w.dts
    echo "};" >> ${S}/arch/arm64/boot/dts/broadcom/bcm2837-rpi-zero-2-w.dts
}