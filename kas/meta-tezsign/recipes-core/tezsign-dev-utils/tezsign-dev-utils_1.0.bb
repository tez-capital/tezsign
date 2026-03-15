SUMMARY = "Tezsign USB Gadget Setup"
LICENSE = "MIT"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://attach-gadget-dev.c \
    file://setup-gadget-dev.c \
"

S = "${WORKDIR}"

do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} setup-gadget-dev.c -o setup-gadget-dev
    ${CC} ${CFLAGS} ${LDFLAGS} attach-gadget-dev.c -o attach-gadget-dev
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 setup-gadget-dev ${D}${bindir}
    install -m 0755 attach-gadget-dev ${D}${bindir}
}