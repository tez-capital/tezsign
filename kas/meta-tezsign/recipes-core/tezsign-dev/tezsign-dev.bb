SUMMARY = "Tezsign Dev"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COREBASE}/meta/files/common-licenses/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://setup-gadget-dev.service \
    file://attach-gadget-dev.service \
    file://10-persistent.conf \
    file://dev-prompt.sh \
    file://tty1-getty.conf \
"

inherit systemd useradd

# Ensure the base core recipe is installed if this is installed
RDEPENDS:${PN} = "tezsign-core tezsign-dev-utils sudo"

# --- Systemd Configuration ---
SYSTEMD_PACKAGES = "${PN}"
SYSTEMD_SERVICE:${PN} = "setup-gadget-dev.service attach-gadget-dev.service"
SYSTEMD_AUTO_ENABLE = "enable"

# --- User Creation Configuration ---
USERADD_PACKAGES = "${PN}"

# 3. Define the user parameters. 
# IMPORTANT: Yocto requires a hashed password, not plain text. 
# See the instructions below on how to generate the hash for "tezsign".
USERADD_PARAM:${PN} = "-m -d /home/dev -s /bin/sh -p '\$6\$NnguhG1R1YiWk3Ry\$A0exAXlAp9PYALEKCSAEaQPIlxhorMjhNOMzhG/V9ceBov8aMArAQ1.zCTwk4XaAfArSWEImaOK4t04jG0YYH1' dev"

do_install() {
     # Install your services
    install -d ${D}${systemd_system_unitdir}
    install -m 0644 ${WORKDIR}/setup-gadget-dev.service ${D}${systemd_system_unitdir}/
    install -m 0644 ${WORKDIR}/attach-gadget-dev.service ${D}${systemd_system_unitdir}/

    # 4. Install the sudoers drop-in file
    install -d ${D}${sysconfdir}/sudoers.d
    # Note: If you want the dev user to run sudo without being prompted for a password, 
    # change the echo below to: echo "dev ALL=(ALL:ALL) NOPASSWD: ALL"
    echo "dev ALL=(ALL:ALL) ALL" > ${D}${sysconfdir}/sudoers.d/01_dev
    
    # Sudoers files strictly require 0440 permissions
    chmod 0440 ${D}${sysconfdir}/sudoers.d/01_dev
}

# 5. Tell Yocto to package the new sudoers file
FILES:${PN} += "${sysconfdir}/sudoers.d/01_dev"

do_install:append() {
    # Install the journald override
    install -d ${D}${sysconfdir}/systemd/journald.conf.d/
    install -m 0644 ${WORKDIR}/10-persistent.conf ${D}${sysconfdir}/systemd/journald.conf.d/

    # Dash does not expand the Bash-style prompt escapes from base-files.
    install -d ${D}${sysconfdir}/profile.d/
    install -m 0644 ${WORKDIR}/dev-prompt.sh ${D}${sysconfdir}/profile.d/
    install -d ${D}/home/dev
    install -m 0644 ${WORKDIR}/dev-prompt.sh ${D}/home/dev/.profile
    install -m 0644 ${WORKDIR}/dev-prompt.sh ${D}/home/dev/.bashrc

    install -d ${D}${sysconfdir}/systemd/system/getty@tty1.service.d/
    install -m 0644 ${WORKDIR}/tty1-getty.conf ${D}${sysconfdir}/systemd/system/getty@tty1.service.d/10-visible-login.conf
}

# Ensure the directory gets packaged
FILES:${PN} += "${sysconfdir}/systemd/journald.conf.d/10-persistent.conf"
FILES:${PN} += "${sysconfdir}/profile.d/dev-prompt.sh"
FILES:${PN} += "/home/dev /home/dev/.profile /home/dev/.bashrc"
FILES:${PN} += "${sysconfdir}/systemd/system/getty@tty1.service.d/10-visible-login.conf"
