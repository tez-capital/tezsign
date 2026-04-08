# meta-rockchip scarthgap currently pins the Radxa Zero 3 U-Boot source to
# a removed rk3xxx-2024.07 revision. Override it here with a live branch head
# that still carries the required Radxa Zero 3 RK3566 defconfig.
SRC_URI:radxa-zero-3 = "git://github.com/Kwiboo/u-boot-rockchip.git;protocol=https;branch=rk3xxx-2025.04;name=Kwiboo"
SRCREV:radxa-zero-3 = "228e9e5b3502ec0e3aac3fae38d9d99f77e9ede1"
SRCREV:radxa-zero-3:rk-u-boot-env = "228e9e5b3502ec0e3aac3fae38d9d99f77e9ede1"
DEPENDS:append:radxa-zero-3 = " gnutls-native"
