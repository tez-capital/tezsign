# TezSign Kernel Bounty - Analysis

## Target Board
- **Radxa Zero 3**
- Architecture: ARM64/AArch64
- Base: Armbian build system

## Current State
- Build uses `KERNEL_CONFIGURE="no"` (default config)
- Removes many packages but keeps kernel bloated
- WiFi/Bluetooth already disabled (radxa-aic8800 extension commented out)

## Bounty Requirements
1. Create minimal kernel config
2. Keep: ext4, fat filesystems
3. Keep: All cryptographic operations
4. Keep: Application-level operations
5. Must boot successfully
6. Must pass all tezsign functionality

## Strategy
1. Start with current config as baseline
2. Remove obvious candidates:
   - Unused filesystems (btrfs, xfs, zfs, ntfs, etc.)
   - Networking drivers (WiFi, Bluetooth - already done)
   - Audio drivers
   - Video/GPU drivers
   - USB gadget drivers (except necessary)
   - Debug features
3. Test build
4. Verify functionality

## What tezsign needs:
- USB gadget mode (already configured)
- Cryptographic operations
- Basic networking (USB ethernet)
- Storage (ext4/fat)
- Serial console

## Key files to create:
- `armbian_userpatches/kernel/radxa-zero3-minimal.config`
- Or kernel patch file

## Build test command:
```bash
./compile.sh \
    BOARD="radxa-zero3" \
    BRANCH="current" \
    KERNEL_CONFIGURE="yes" \
    ...
```
This opens menuconfig for editing.

