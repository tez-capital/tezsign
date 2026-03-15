KVER := "${PV}"
require linux-mainline.inc
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git;branch=master;protocol=https"
SRCREV = "0ff41df1cb268fc69e703a08a57ee14ae967d0ca"
SRC_URI:append:raspberrypi0-2w-mainline = " file://defconfig"

PV = "6.18"