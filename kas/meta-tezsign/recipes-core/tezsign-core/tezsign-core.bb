SUMMARY = "Tezsign Core"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COREBASE}/meta/files/common-licenses/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://setup-gadget.service \
    file://attach-gadget.service \
    file://generate-serial.service \
    file://tezsign.service \
    file://ffs_registrar.service \
    file://io-scheduler.conf \
    file://99-io-performance.rules \
"

inherit externalsrc goarch systemd useradd

DEPENDS += "go-native"

TEZSIGN_REPO_ROOT ?= "${@os.path.abspath(os.path.join(d.getVar('THISDIR'), '../../../..'))}"
EXTERNALSRC = "${TEZSIGN_REPO_ROOT}/app"
EXTERNALSRC_BUILD = "${WORKDIR}/build"

python () {
    import os

    repo_root = d.getVar("TEZSIGN_REPO_ROOT")
    go_mod = os.path.join(repo_root, "go.mod")
    registrar_dir = os.path.join(repo_root, "app", "ffs_registrar")
    if os.path.isfile(go_mod) and os.path.isdir(registrar_dir):
        return

    bb.fatal(
        "%s: TEZSIGN_REPO_ROOT=%s does not point at the tezsign repository root. "
        "When building inside a container, mount the repository root and export "
        "TEZSIGN_REPO_ROOT to that mounted path." % (d.getVar("PN"), repo_root)
    )
}

# Systemd configuration
SYSTEMD_PACKAGES = "${PN}"
SYSTEMD_SERVICE:${PN} = "setup-gadget.service attach-gadget.service ffs_registrar.service tezsign.service generate-serial.service"
SYSTEMD_AUTO_ENABLE = "enable"

# Create the users and groups your script requires
USERADD_PACKAGES = "${PN}"
GROUPADD_PARAM:${PN} = "-r dev_manager; -r registrar; -r tezsign"
USERADD_PARAM:${PN} = " \
    -r -g registrar -G dev_manager registrar; \
    -r -g tezsign -G dev_manager tezsign \
"

INHIBIT_PACKAGE_STRIP = "1"
INHIBIT_PACKAGE_DEBUG_SPLIT = "1"
do_unpack[nostamp] = "1"
do_install[depends] += "virtual/${TARGET_PREFIX}binutils:do_populate_sysroot"

do_configure() {
    :
}

do_compile[network] = "1"
do_compile[nostamp] = "1"
do_compile() {
    install -d ${B} ${WORKDIR}/home ${WORKDIR}/go-cache ${WORKDIR}/go-mod-cache
    rm -rf ${WORKDIR}/go-cache/*

    export HOME="${WORKDIR}/home"
    export GOCACHE="${WORKDIR}/go-cache"
    export GOMODCACHE="${WORKDIR}/go-mod-cache"
    export GOOS="${TARGET_GOOS}"
    export GOARCH="${TARGET_GOARCH}"
    export CGO_ENABLED="0"

    cd ${S}
    go build -a -trimpath -buildvcs=false \
        -ldflags='-s -w -buildid=' \
        -o ${B}/ffs_registrar \
        ./ffs_registrar
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${B}/ffs_registrar ${D}${bindir}/ffs_registrar
    ${STRIP} --strip-all ${D}${bindir}/ffs_registrar

    # Install the systemd service file
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/setup-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/attach-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/ffs_registrar.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/tezsign.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/generate-serial.service ${D}${systemd_system_unitdir}/

    install -d ${D}${sysconfdir}/tmpfiles.d
    install -m 0644 ${WORKDIR}/io-scheduler.conf ${D}${sysconfdir}/tmpfiles.d/

    # Install the UDEV rules
    install -d ${D}${sysconfdir}/udev/rules.d
    install -m 0644 ${WORKDIR}/99-io-performance.rules ${D}${sysconfdir}/udev/rules.d/
}
