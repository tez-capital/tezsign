# Minimal Kernel Configuration for Radxa Zero 3

This directory contains a minimal kernel configuration for the Radxa Zero 3 (RK3566) specifically optimized for tezsign.

## Overview

This minimal kernel configuration strips down the Linux kernel to the bare essentials required for tezsign operation while maximizing security and performance by:

- **Removing unused filesystems**: Only ext4 and vfat are enabled
- **Disabling networking**: WiFi, Bluetooth, Ethernet drivers removed (tezsign is air-gapped)
- **Removing multimedia**: Audio and video drivers disabled
- **Minimizing device support**: Only essential USB and storage support
- **Reducing attack surface**: Disabled debugging, profiling, and unnecessary features
- **Optimizing for embedded**: Reduced kernel size and memory footprint

## What's Included

### Core Requirements
- ARM64/AArch64 architecture support for RK3566
- Basic platform support (Rockchip RK3566 SoC)
- USB Device Controller (DWC3) for gadget mode
- USB Gadget framework with ConfigFS
- FunctionFS support for custom USB gadget
- ECM (Ethernet Control Model) for dev mode
- USB PHY and role switching support
- Device Tree (OF) support for proper hardware initialization

### File Systems
- ext4 (for rootfs, app partition)
- vfat/FAT32 (for boot partition, data partition)
- tmpfs, sysfs, procfs, devtmpfs (system requirements)
- configfs (for USB gadget configuration)

### Cryptography
- Full crypto subsystem for signing operations
- Hash algorithms: SHA1, SHA256, SHA512, SHA3, BLAKE2B, BLAKE2S
- Ciphers: AES (with ARM64 CE acceleration), ChaCha20, Poly1305
- Hardware crypto acceleration (Rockchip crypto engine)
- Hardware random number generation (Rockchip RNG)
- DRBG, Jitterentropy for secure random generation
- Crypto user API for userspace access

### Essential Drivers
- MMC/SD card support (for boot device)
- USB support (host and gadget mode with DWC3 controller)
- USB PHY (Rockchip INNO USB2, Type-C PHY)
- GPIO and I2C (for basic board functionality)
- SPI support (Rockchip SPI and SFC controllers)
- Serial console (8250 UART for debugging)
- DMA Engine (PL330 for high-performance transfers)
- RTC support (RK808 RTC for timestamping)
- Watchdog timer (system reliability)
- PWM support (for power regulation)
- Thermal management (CPU thermal zones)

## What's Removed

### Filesystems
- All unused filesystems (btrfs, xfs, zfs, f2fs, jffs2, squashfs, etc.)
- Network filesystems (NFS, CIFS, CEPH, AFS, 9P)
- Overlay and FUSE filesystems
- ISO9660, UDF, NTFS

### Networking
- Complete network stack (TCP/IP)
- All network drivers (Ethernet, WiFi, Bluetooth)
- Wireless support (802.11 a/b/g/n/ac/ax)
- Network protocols (IPv6, CAN, XFRM)
- Netfilter, bridging, VLANs

### Multimedia
- Sound/Audio subsystem (ALSA, SoC audio)
- Video/Graphics drivers (DRM, framebuffer)
- Video4Linux (V4L2)
- Media support

### Input/Output
- Most input devices (mouse, touchscreen, tablet, joystick)
- USB HID devices
- Industrial I/O (IIO)
- LED subsystem

### Development & Debug
- Kernel debugging interfaces (debugfs, kprobes)
- Profiling tools (ftrace, perf)
- KALLSYMS (symbol table)
- Debug info and SLUB debug
- BPF JIT and syscall interface

### Security/Isolation
- SELinux, SMACK, AppArmor, TOMOYO
- User namespaces
- Container features
- Checkpoint/restore

### Hardware Features
- Virtualization (KVM)
- RAID, DM, LVM
- Most hardware monitoring (HWMON)
- MTD (Memory Technology Devices)
- PCMCIA, CardBus
- NFC, IrDA
- Remote processors (remoteproc, rpmsg)
- ACPI, EFI (not used on ARM)
- Power supply class
- Unnecessary block devices (loop, NBD, RAM disk)

## Configuration Details

The configuration is based on the standard Armbian kernel config for Rockchip64 boards but heavily stripped down. Key configuration decisions:

1. **Kernel Compression**: Using LZ4 for fast boot times
2. **Preemption Model**: Voluntary for better throughput
3. **Timer Frequency**: 250Hz (good balance for embedded)
4. **Security Features**: 
   - Seccomp and seccomp filters enabled
   - Stack protection (STACKPROTECTOR_STRONG)
   - Hardened usercopy and FORTIFY_SOURCE
   - Strict kernel/module RWX
   - User namespaces disabled
   - BPF JIT disabled
   - Coredumps disabled
5. **Module Support**: Minimized, most features built-in, module unloading disabled
6. **CPU Frequency**: Schedutil governor with performance/powersave options
7. **Power Management**: Full PM support with suspend and runtime PM

## File Structure

- `config-minimal.fragment`: Kernel config fragment with minimal settings
- `README.md`: This file

## How to Use

This configuration is automatically applied when building with the Armbian build system if placed in the correct location:

```
armbian_userpatches/kernel/rockchip64-current/config-minimal.fragment
```

The Armbian build system will merge this fragment with the base configuration.

## Testing

After building and flashing the image:

1. Verify boot process completes successfully
2. Check that USB gadget functionality works (`lsmod | grep usb_f_fs`)
3. Confirm file systems mount correctly (`mount | grep -E 'ext4|vfat'`)
4. Test tezsign initialization and signing operations
5. Verify all cryptographic operations work

## Size Comparison

Expected kernel size reductions:
- Standard kernel: ~8-10 MB
- Minimal kernel: ~3-5 MB (estimated 50-60% reduction)

## Known Limitations

- No network connectivity (by design - air-gapped security)
- No display output (headless operation only)
- No audio support
- Serial console required for direct debugging
- Dev images use USB ECM for network access

## Version Info

- Target Board: Radxa Zero 3
- SoC: Rockchip RK3566
- Architecture: ARM64/AArch64
- Kernel Branch: current (6.6.x or newer)
- Created: 2025-11-11

## Contributing

If you find that additional kernel features are required for tezsign operation, please document them and submit an updated configuration.

## Security Considerations

This minimal configuration enhances security by:

### Attack Surface Reduction
- Removing entire network stack (no remote exploits possible)
- Disabling all wireless and Bluetooth drivers
- Removing multimedia subsystems (no codec vulnerabilities)
- Eliminating debug and profiling interfaces
- Disabling unused filesystems and drivers

### Kernel Hardening
- Stack overflow protection (STACKPROTECTOR_STRONG)
- Strict kernel/module memory permissions (RWX separation)
- Hardened usercopy (buffer overflow protection)
- FORTIFY_SOURCE (additional bounds checking)
- Seccomp filters for syscall restriction
- User namespaces disabled (privilege escalation prevention)
- BPF JIT disabled (JIT-spray attack prevention)
- Coredumps disabled (information disclosure prevention)

### Minimal Module Loading
- Module unloading disabled (prevents runtime tampering)
- Module signing not enforced but supported
- Most features built-in (reduces module attack surface)

### Air-Gap Enforcement
- Complete network stack removed at kernel level
- No network device drivers compiled in
- Physical isolation enforced by hardware configuration

## Performance Benefits

- Faster boot times (less kernel initialization)
- Lower memory footprint
- Reduced CPU overhead from disabled subsystems
- Smaller kernel image fits better in cache

