require linux-mainline.inc
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git;protocol=https;branch=linux-6.18.y"
SRCREV = "${AUTOREV}"

# Keep the current Raspberry Pi family on a shared base defconfig, then layer
# family and board-specific fragments on top. Dev builds append dedicated dev-only
# fragments so production images can stay aggressively minimal.
SRC_URI:append = " \
    file://defconfig \
    file://0001-dwc2-gadget-skip-stop-xfr-on-active-dequeue.patch \
    file://tezsign-common.cfg \
    file://tezsign-common-dev.cfg \
    file://rpi-common.cfg \
    file://rpi-zero2w.cfg \
    file://rpi4.cfg \
    file://rpi-dev-common.cfg \
    file://rpi-zero2w-dev.cfg \
    file://rpi4-dev.cfg \
    file://radxa-common.cfg \
    file://radxa-dev-common.cfg \
    file://radxa-zero3.cfg \
    file://radxa-zero3-dev.cfg \
    file://0002-arm64-dts-rockchip-radxa-zero-3w-usb-peripheral.patch \
"

PV = "6.18+git${SRCPV}"

TEZSIGN_KERNEL_CONFIG_FRAGMENTS = ""
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:raspberrypi0-2w-tezsign = "tezsign-common.cfg rpi-common.cfg rpi-zero2w.cfg"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:append:raspberrypi0-2w-tezsign = "${@' tezsign-common-dev.cfg rpi-dev-common.cfg rpi-zero2w-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:raspberrypi4-tezsign = "tezsign-common.cfg rpi-common.cfg rpi4.cfg"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:append:raspberrypi4-tezsign = "${@' tezsign-common-dev.cfg rpi-dev-common.cfg rpi4-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''}"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:radxa-zero3-tezsign = "tezsign-common.cfg radxa-common.cfg radxa-zero3.cfg"
TEZSIGN_KERNEL_CONFIG_FRAGMENTS:append:radxa-zero3-tezsign = "${@' tezsign-common-dev.cfg radxa-dev-common.cfg radxa-zero3-dev.cfg' if d.getVar('TEZSIGN_DEV') == '1' else ''}"

do_configure:append() {
    fragments=""

    for fragment in ${TEZSIGN_KERNEL_CONFIG_FRAGMENTS}; do
        if [ -f "${WORKDIR}/${fragment}" ]; then
            fragments="${fragments} ${WORKDIR}/${fragment}"
        fi
    done

    if [ -n "${fragments}" ]; then
        # Start from allnoconfig so only options explicitly listed in
        # defconfig + fragments are enabled.  The default kernel class
        # expands defconfig via olddefconfig filling thousands of
        # unspecified options with kernel defaults (often =y).
        oe_runmake -C ${S} O=${B} allnoconfig
        ${S}/scripts/kconfig/merge_config.sh -m -O ${B} ${B}/.config ${WORKDIR}/defconfig ${fragments}
        oe_runmake -C ${S} O=${B} olddefconfig
    fi
}
