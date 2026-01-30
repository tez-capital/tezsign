# TezSign Kernel Minimization - Implementation Plan

## Goal
Create minimal kernel config for Radxa Zero 3 that:
- Boots successfully
- Supports tezsign functionality
- Minimizes attack surface
- Reduces resource usage

## Current Baseline
- Board: Radxa Zero 3
- Architecture: ARM64 (bcm2711 family)
- Kernel: 6.12 (current branch)
- Build system: Armbian

## What tezsign Needs (from code analysis)

### USB Gadget Mode
- USB DWC2 driver (already patched in repo)
- USB Ethernet (ECM)
- Serial gadget (for console)

### Cryptographic Operations
- Hardware crypto if available
- Software crypto fallback
- Key management

### Storage
- ext4 (root filesystem)
- fat/vfat (boot)

### Basic System
- Serial console
- USB host (for device mode)
- Minimal networking (USB ethernet)

## What to REMOVE

### Filesystems (keep only ext4/fat)
- btrfs
- xfs
- zfs
- ntfs
- f2fs
- squashfs (maybe keep for initrd?)

### Networking (keep only USB)
- WiFi drivers (aic8800 already disabled)
- Bluetooth (already disabled)
- Ethernet PHY drivers (except USB)
- Wireless stack

### Audio/Video
- All sound drivers (ALSA, etc.)
- All GPU drivers
- All display drivers
- Camera drivers

### Input Devices
- Touchscreens
- Tablets
- Joysticks
- Most HID (keep keyboard for dev)

### Other
- Debug features (if not needed)
- Unused architectures
- Profiling tools
- Tracing (unless debug needed)

## Implementation Steps

### Step 1: Get Current Config
```bash
cd armbian/build
./compile.sh \
    BOARD=radxa-zero3 \
    BRANCH=current \
    KERNEL_CONFIGURE=yes
# This opens menuconfig, save current as baseline
```

### Step 2: Create Minimal Config
- Start from saved config
- Remove filesystems
- Remove networking
- Remove audio/video
- Test build

### Step 3: Verify Functionality
- Boot test
- USB gadget test
- Crypto operation test
- tezsign app test

### Step 4: Document & Submit
- Create .config file
- Document changes
- Submit PR with explanation

## Expected Size Reduction
- Current: ~15-20MB kernel
- Target: ~8-12MB kernel
- Modules: Reduce significantly

## Risk Mitigation
- Keep backup of working config
- Test incrementally
- Document each removal
- Have rollback plan

## Timeline
- Analysis: 2 hours ✓ (done)
- Config creation: 4-6 hours
- Testing: 4-6 hours
- Documentation: 2 hours
- Total: 12-16 hours

## Reward
- 100 XTZ (~$60-80 USD at current prices)
- Win condition: Smallest functional kernel
- Deadline: January 2026 (URGENT)

