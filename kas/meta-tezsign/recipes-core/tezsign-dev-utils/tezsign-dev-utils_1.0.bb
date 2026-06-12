SUMMARY = "Tezsign USB Gadget Setup"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://attach-gadget-dev.c \
    file://setup-gadget-dev.c \
"

S = "${UNPACKDIR}"

do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/setup-gadget-dev.c -o ${B}/setup-gadget-dev
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/attach-gadget-dev.c -o ${B}/attach-gadget-dev
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${B}/setup-gadget-dev ${D}${bindir}
    install -m 0755 ${B}/attach-gadget-dev ${D}${bindir}
}