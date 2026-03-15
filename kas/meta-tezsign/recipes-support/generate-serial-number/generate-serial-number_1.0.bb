SUMMARY = "Stable ASCII USB serial generator"
LICENSE = "MIT"
LIC_FILES_CHKSUM = "file://${COMMON_LICENSE_DIR}/MIT;md5=0835ade698e0bcf8506ecda2f7b4f302"

SRC_URI = "file://generate-serial-number.c"

S = "${WORKDIR}"

do_compile() {
    ${CC} ${CFLAGS} ${LDFLAGS} generate-serial-number.c -o generate-serial-number
}

do_install() {
    install -d ${D}${bindir}
    install -m 0755 generate-serial-number ${D}${bindir}
}