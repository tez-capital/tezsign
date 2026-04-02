SUMMARY = "Raw files for the app partition"
LICENSE = "CLOSED"

INHIBIT_DEFAULT_DEPS = "1"

SRC_URI = " \
    file://tezsign \
"

inherit deploy

do_configure[noexec] = "1"
do_compile[noexec] = "1"
do_install[noexec] = "1"
do_deploy[depends] += "virtual/${TARGET_PREFIX}binutils:do_populate_sysroot"

do_deploy() {
    install -d ${DEPLOYDIR}/appfs
    install -m 0755 ${WORKDIR}/tezsign ${DEPLOYDIR}/appfs/tezsign
    # Normalize the prebuilt gadget binary before it lands in appfs.
    ${STRIP} ${DEPLOYDIR}/appfs/tezsign
}

addtask deploy after do_compile before do_build
