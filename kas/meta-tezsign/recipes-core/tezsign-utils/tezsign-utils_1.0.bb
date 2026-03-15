SUMMARY = "Tezsign USB Gadget Setup"
LICENSE = "MIT"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = "file://setup-gadget.c"

S = "${WORKDIR}"

do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} setup-gadget.c -o setup-gadget
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 setup-gadget ${D}${bindir}
}