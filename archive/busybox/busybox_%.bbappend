FILESEXTRAPATHS:prepend := "${THISDIR}/files:"
SRC_URI += "file://no-bloat.cfg"

SYSTEMD_SERVICE:${PN}-syslog = ""

PACKAGECONFIG:remove = "syslog udhcpc"