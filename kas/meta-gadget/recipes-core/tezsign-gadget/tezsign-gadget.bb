SUMMARY = "Tezsign USB Composite Gadget Systemd Setup"
LICENSE = "MIT"
LIC_FILES_CHKSUM = "file://${COREBASE}/meta/files/common-licenses/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://setup-gadget.sh \
    file://setup-gadget.service \
    file://attach-gadget.sh \
    file://attach-gadget.service \
    file://generate-serial-number.sh \
    file://generate-serial.service \
    file://setup-gadget-dev.sh \
    file://setup-gadget-dev.service \
    file://attach-gadget-dev.sh \
    file://attach-gadget-dev.service \
    file://tezsign.service \
    file://cpu-tuning.conf \
    file://99-io-performance.rules \
"

# Inherit systemd and user creation
inherit systemd useradd

PACKAGES =+ "${PN}-ecm"
FILES:${PN}-ecm = " \
    ${bindir}/setup-gadget-dev.sh \
    ${systemd_system_unitdir}/setup-gadget-dev.service \
    ${bindir}/attach-gadget-dev.sh \
    ${systemd_system_unitdir}/attach-gadget-dev.service \
"


# Systemd configuration
SYSTEMD_PACKAGES = "${PN} ${PN}-ecm"
SYSTEMD_SERVICE:${PN} = "setup-gadget.service attach-gadget.service tezsign.service generate-serial.service"
SYSTEMD_SERVICE:${PN}-ecm = "setup-gadget-dev.service"
SYSTEMD_AUTO_ENABLE = "enable"

# Create the users and groups your script requires
USERADD_PACKAGES = "${PN}"
GROUPADD_PARAM:${PN} = "-r dev_manager; -r registrar; -r tezsign"
USERADD_PARAM:${PN} = "-r -g registrar registrar; -r -g tezsign tezsign"

do_install() {
    # Install the POSIX shell script
    install -d ${D}${bindir}
    install -m 0755 ${WORKDIR}/setup-gadget.sh ${D}${bindir}/setup-gadget.sh
    install -m 0755 ${WORKDIR}/attach-gadget.sh ${D}${bindir}/attach-gadget.sh
    install -m 0755 ${WORKDIR}/generate-serial-number.sh ${D}${bindir}/generate-serial-number.sh
    install -m 0755 ${WORKDIR}/setup-gadget-dev.sh ${D}${bindir}/
    install -m 0755 ${WORKDIR}/attach-gadget-dev.sh ${D}${bindir}/

    # Install the systemd service file
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/setup-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/attach-gadget.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/tezsign.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/generate-serial.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/setup-gadget-dev.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/attach-gadget-dev.service ${D}${systemd_system_unitdir}/

    # Install the CPU tuning tmpfiles configuration
    install -d ${D}${sysconfdir}/tmpfiles.d
    install -m 0644 ${WORKDIR}/cpu-tuning.conf ${D}${sysconfdir}/tmpfiles.d/

    # Install the UDEV rules
    install -d ${D}${sysconfdir}/udev/rules.d
    install -m 0644 ${WORKDIR}/99-io-performance.rules ${D}${sysconfdir}/udev/rules.d/
}