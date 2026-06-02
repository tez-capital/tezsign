SUMMARY = "Tezsign USB Gadget Setup"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = " \
    file://setup-gadget.c \
    file://attach-gadget.c \
    file://tune-interrupts.c \
    file://99-led-heartbeat.rules \
"

S = "${UNPACKDIR}"

do_compile[nostamp] = "1"
do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/setup-gadget.c -o ${B}/setup-gadget
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/attach-gadget.c -o ${B}/attach-gadget
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/tune-interrupts.c -o ${B}/tune-interrupts
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${B}/setup-gadget ${D}${bindir}
    install -m 0755 ${B}/attach-gadget ${D}${bindir}
    install -m 0755 ${B}/tune-interrupts ${D}${bindir}

    install -d ${D}${sysconfdir}/udev/rules.d
    install -m 0644 ${UNPACKDIR}/99-led-heartbeat.rules ${D}${sysconfdir}/udev/rules.d/
}