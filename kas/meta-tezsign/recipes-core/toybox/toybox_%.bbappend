FILESEXTRAPATHS:prepend := "${THISDIR}/files:"
SRC_URI += "file://defconfig"

do_install:append() {
    if [ -f ${D}${sysconfdir}/toybox.links ]; then
        sed -i -e '/\/sbin\//d' -e '/\/sbin$/d' ${D}${sysconfdir}/toybox.links
        sed -i -e 's#^/bin/#/usr/bin/#' ${D}${sysconfdir}/toybox.links
        sort -u ${D}${sysconfdir}/toybox.links -o ${D}${sysconfdir}/toybox.links
    fi
}