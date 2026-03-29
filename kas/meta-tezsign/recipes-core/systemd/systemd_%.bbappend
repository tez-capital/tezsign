# Production images are fully headless; keep login/getty tooling only in dev builds.
RDEPENDS:${PN}:remove = "${@bb.utils.contains('TEZSIGN_DEV', '1', '', 'shadow util-linux-agetty', d)}"
