KVER := "${PV}"
require linux-mainline.inc
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git;branch=master;protocol=https"
SRCREV = "0ff41df1cb268fc69e703a08a57ee14ae967d0ca"

# Keep the current Raspberry Pi family on a shared base defconfig, then layer
# family and board-specific fragments on top. Dev builds append dedicated dev-only
# fragments so production images can stay aggressively minimal.
SRC_URI:append = " \
    file://rpi-common.cfg \
    file://rpi-zero2w.cfg \
    file://rpi4.cfg \
    file://rpi-dev-common.cfg \
    file://rpi-zero2w-dev.cfg \
    file://rpi4-dev.cfg \
    file://radxa-zero3.cfg \
"
SRC_URI:append:raspberrypi0-2w-tezsign = " file://defconfig"
SRC_URI:append:raspberrypi4-tezsign = " file://defconfig"

PV = "6.18"

TEZSIGN_KERNEL_CONFIG_FRAGMENTS = ""
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:raspberrypi0-2w-tezsign = "rpi-common.cfg rpi-zero2w.cfg"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:append:raspberrypi0-2w-tezsign = "${@' rpi-dev-common.cfg rpi-zero2w-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:raspberrypi4-tezsign = "rpi-common.cfg rpi4.cfg"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:append:raspberrypi4-tezsign = "${@' rpi-dev-common.cfg rpi4-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:radxa-zero3-tezsign = "radxa-zero3.cfg"

do_configure:append() {
    fragments=""

    for fragment in ${TEZSIGN_KERNEL_CONFIG_FRAGMENTS}; do
        if [ -f "${WORKDIR}/${fragment}" ]; then
            fragments="${fragments} ${WORKDIR}/${fragment}"
        fi
    done

    if [ -n "${fragments}" ]; then
        ${S}/scripts/kconfig/merge_config.sh -m -O ${B} ${B}/.config ${fragments}
        oe_runmake -C ${S} O=${B} olddefconfig
    fi
}
