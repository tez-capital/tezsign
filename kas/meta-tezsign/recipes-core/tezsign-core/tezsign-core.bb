SUMMARY = "Tezsign Core"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COREBASE}/meta/files/common-licenses/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://setup-gadget.service \
    file://attach-gadget.service \
    file://generate-serial.service \
    file://tezsign.service \
    file://ffs_registrar \
    file://ffs_registrar.service \
    file://cpu-tuning.conf \
    file://99-io-performance.rules \
"

inherit systemd useradd

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

do_install() {
    # Install the POSIX shell script
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/ffs_registrar ${D}${bindir}/ffs_registrar

    # Install the systemd service file
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/setup-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/attach-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/ffs_registrar.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/tezsign.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/generate-serial.service ${D}${systemd_system_unitdir}/

    # Install the CPU tuning tmpfiles configuration
    install -d ${D}${sysconfdir}/tmpfiles.d
    install -m 0644 ${WORKDIR}/cpu-tuning.conf ${D}${sysconfdir}/tmpfiles.d/

    # Install the UDEV rules
    install -d ${D}${sysconfdir}/udev/rules.d
    install -m 0644 ${WORKDIR}/99-io-performance.rules ${D}${sysconfdir}/udev/rules.d/
}