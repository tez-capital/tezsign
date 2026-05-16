# Production images are fully headless; keep login/getty tooling only in dev builds.
RDEPENDS:${PN}:remove = "${@bb.utils.contains('TEZSIGN_DEV', '1', '', 'shadow util-linux-agetty', d)}"

FILESEXTRAPATHS:prepend := "${THISDIR}/files:"

# Poky's scarthgap musl strndupa patch can fail on udev-builtin-net_id.c
# during GitHub clean builds. Keep the patch's other musl fixes and add the
# net_id include idempotently below.
SRC_URI_MUSL:remove = "file://0003-src-basic-missing.h-check-for-missing-strndupa.patch"
SRC_URI_MUSL:append = " file://0003-src-basic-missing.h-check-for-missing-strndupa-no-net-id.patch"

python do_patch:append:libc-musl() {
    from pathlib import Path

    path = Path(d.getVar("S")) / "src/udev/udev-builtin-net_id.c"
    include = '#include "missing_stdlib.h"'
    anchor = '#include "udev-builtin.h"'
    text = path.read_text()

    if include not in text:
        if anchor not in text:
            bb.fatal(f"Could not add {include} to {path}: missing anchor {anchor}")
        path.write_text(text.replace(anchor, f"{anchor}\n{include}", 1))
}

# Remove all default PACKAGECONFIG values and set only the bare minimum
# kmod: Required for loading kernel modules (essential for many services)
# logind: Basic session management (dev only)
# zstd: Recommended for journal compression (remains small)
# set-time-epoch: Prevents time-travel issues on systems without RTC
PACKAGECONFIG = " \
    kmod \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', '', 'logind', d)} \
    set-time-epoch \
    zstd \
"

# Force-remove specific features that might be pulled in by DISTRO_FEATURES
# This ensures networking, containers, and advanced tools are NOT built.
PACKAGECONFIG:remove = " \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', '', 'networkd', d)} \
    resolved \
    timesyncd \
    nss-resolve \
    nss-mymachines \
    hostnamed \
    machined \
    portabled \
    coredump \
    pstore \
    binfmt \
    repart \
    homed \
    importd \
    efi \
    bootloader \
    myhostname \
    localed \
    vconsole \
    quotacheck \
    hibernate \
    ima \
    smack \
    selinux \
    audit \
    acl \
    sysvinit \
"

# Disable the installation of the hardware database (saves ~5MB to 10MB)
# and other non-essential helper packages.
RRECOMMENDS:${PN}:remove = " \
    ${PN}-extra-utils \
    ${PN}-vconsole-setup \
    udev-hwdb \
    ${PN}-analyze \
    ${PN}-zsh-completion \
"

# Ensure the build doesn't try to include networking-related configs
EXTRA_OEMESON += "-Dnetworkd=false -Dresolve=false -Dtimesyncd=false"
