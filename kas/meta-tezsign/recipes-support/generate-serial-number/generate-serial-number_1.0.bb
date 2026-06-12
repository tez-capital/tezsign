SUMMARY = "Stable ASCII USB serial generator"
LICENSE = "CLOSED"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = "file://generate-serial-number.c"

S = "${UNPACKDIR}"

do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} ${S}/generate-serial-number.c -o ${B}/generate-serial-number
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 ${B}/generate-serial-number ${D}${bindir}
}