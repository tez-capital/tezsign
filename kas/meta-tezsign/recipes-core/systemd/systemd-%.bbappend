# Remove all default PACKAGECONFIG values and set only the bare minimum
# kmod: Required for loading kernel modules (essential for many services)
# logind: Basic session management
# zstd: Recommended for journal compression (remains small)
# set-time-epoch: Prevents time-travel issues on systems without RTC
PACKAGECONFIG:pn-systemd = " \
    kmod \
    ${@bb.utils.contains('TEZSIGN_DEV', '1', '', 'logind', d)} \
    set-time-epoch \
    zstd \
"

# Force-remove specific features that might be pulled in by DISTRO_FEATURES
# This ensures networking, containers, and advanced tools are NOT built.
PACKAGECONFIG:remove:pn-systemd = " \
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
