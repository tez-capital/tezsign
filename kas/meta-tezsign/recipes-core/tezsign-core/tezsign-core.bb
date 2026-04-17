SUMMARY = "Tezsign Core"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COREBASE}/meta/files/common-licenses/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://app.mount \
    file://data.mount \
    file://setup-gadget.service \
    file://attach-gadget.service \
    file://generate-serial.service \
    file://tezsign.service \
    file://ffs_registrar \
    file://ffs_registrar.service \
    file://io-scheduler.conf \
    file://99-io-performance.rules \
"

inherit systemd useradd

# Systemd configuration
SYSTEMD_PACKAGES = "${PN}"
SYSTEMD_SERVICE:${PN} = "app.mount data.mount setup-gadget.service attach-gadget.service ffs_registrar.service tezsign.service generate-serial.service"
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
do_install[depends] += "virtual/${TARGET_PREFIX}binutils:do_populate_sysroot"

do_install() {
    # Install the POSIX shell script
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/ffs_registrar ${D}${bindir}/ffs_registrar
    # Normalize the prebuilt registrar binary before packaging.
    ${STRIP} ${D}${bindir}/ffs_registrar

    # Install the systemd service file
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/app.mount ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/data.mount ${D}${systemd_system_unitdir}/
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
