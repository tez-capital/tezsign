DESCRIPTION = "Mainline Linux kernel"
LICENSE = "GPL-2.0-only"
LIC_FILES_CHKSUM = "file://COPYING;md5=6bc538ed5bd9a7fc9398086aedcd7e46"
inherit kernel

S = "${WORKDIR}/git"
SRC_URI = "git://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git;branch=linux-${KVER}.y;protocol=https"
SRCREV = "${AUTOREV}"
PV = "${KVER}+git"
PROVIDES = "virtual/kernel"
