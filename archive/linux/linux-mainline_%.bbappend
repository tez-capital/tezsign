FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

SRC_URI += "file://stripped.cfg"

# Safely append the USB peripheral override using a shell task
# (Checking if the file exists first, just in case meta-mainline uses a slightly different DTB path)
do_configure:prepend() {
    DTS_FILE="${S}/arch/arm64/boot/dts/broadcom/bcm2837-rpi-zero-2-w.dts"
    if [ -f "$DTS_FILE" ]; then
        echo "" >> "$DTS_FILE"
        echo "&usb {" >> "$DTS_FILE"
        echo "    dr_mode = \"peripheral\";" >> "$DTS_FILE"
        echo "};" >> "$DTS_FILE"
    fi
}