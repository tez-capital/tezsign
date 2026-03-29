# Drop CLI dbus helpers from the appliance image. Keep the daemon/socket pieces.
RDEPENDS:${PN}:remove = "dbus-tools"
