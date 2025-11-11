# Testing Guide for Minimal Kernel Configuration

This document provides comprehensive testing procedures for the minimal kernel configuration for Radxa Zero 3.

## Prerequisites

Before testing, ensure you have:
- Radxa Zero 3 board
- High-quality SD card (4GB or larger)
- USB-C data cable
- Host machine with tezsign host app installed
- Serial console adapter (optional, for debugging)

## Building the Kernel

### Using Armbian Build System

1. Clone the Armbian build repository:
```bash
git clone https://github.com/armbian/build.git
cd build
```

2. Copy the tezsign userpatches:
```bash
cp -r /path/to/tezsign/armbian_userpatches/* userpatches/
```

3. Build the image with the minimal kernel config:
```bash
./compile.sh \
    BOARD=radxa-zero3 \
    BRANCH=current \
    RELEASE=trixie \
    BUILD_MINIMAL=yes \
    BUILD_DESKTOP=no \
    KERNEL_CONFIGURE=no
```

The minimal kernel configuration fragment will be automatically applied during the build process.

### Expected Build Output

- Kernel size should be significantly smaller than standard build (~3-5 MB vs ~8-10 MB)
- Build log should show configuration fragment being applied
- No errors related to missing dependencies

## Testing Checklist

### 1. Boot Test
- [ ] Device boots successfully
- [ ] Boot completes within reasonable time (< 60 seconds)
- [ ] No kernel panics in dmesg
- [ ] All partitions mount correctly

```bash
# Check boot messages
dmesg | grep -i error

# Verify partitions
mount | grep -E 'ext4|vfat'

# Expected output:
# /dev/mmcblk0p2 on / type ext4 (ro,relatime)
# /dev/mmcblk0p1 on /boot type vfat (ro,relatime,...)
# LABEL=TEZSIGN_APP on /app type vfat (ro,exec,...)
# LABEL=TEZSIGN_DATA on /data type vfat (rw,...)
```

### 2. USB Gadget Functionality
- [ ] USB gadget modules load successfully
- [ ] ConfigFS is available
- [ ] FunctionFS mounts correctly
- [ ] Device is recognized by host

```bash
# Check loaded USB modules
lsmod | grep -E 'usb_f|libcomposite|configfs'

# Expected modules:
# usb_f_fs
# usb_f_ecm (dev mode only)
# libcomposite
# configfs

# Check configfs mount
mount | grep configfs
# Expected: configfs on /sys/kernel/config type configfs (rw,relatime)

# Check FunctionFS
ls -la /dev/ffs/tezsign/
# Expected: ep0, ep1, ep2, ep3, ep4

# Check USB gadget configuration
ls /sys/kernel/config/usb_gadget/g1/
```

### 3. Cryptographic Support
- [ ] Crypto subsystem initialized
- [ ] Required algorithms available
- [ ] Hardware RNG functional
- [ ] Hardware crypto acceleration working

```bash
# Check available crypto algorithms
cat /proc/crypto | grep -E 'name|driver' | head -40

# Verify required algorithms for Tezos
cat /proc/crypto | grep -A 2 "name.*sha256"
cat /proc/crypto | grep -A 2 "name.*blake2b"
cat /proc/crypto | grep -A 2 "name.*sha3"

# Verify RNG
cat /proc/sys/kernel/random/entropy_avail
# Should show reasonable entropy (> 100)

# Test hardware RNG
ls -la /dev/hwrng
cat /sys/class/misc/hw_random/rng_available
cat /sys/class/misc/hw_random/rng_current

# Test basic crypto operation
dd if=/dev/urandom of=/tmp/test.bin bs=1M count=1
sha256sum /tmp/test.bin
rm /tmp/test.bin

# Check for Rockchip crypto driver
dmesg | grep -i rockchip | grep -i crypto
```

### 4. Storage Support
- [ ] SD card detected
- [ ] All partitions accessible
- [ ] Read/write operations work
- [ ] No filesystem errors

```bash
# Check block devices
lsblk

# Expected output:
# mmcblk0     179:0    0 3.7G  0 disk 
# ├─mmcblk0p1 179:1    0  64M  0 part /boot
# ├─mmcblk0p2 179:2    0 2.2G  0 part /
# ├─mmcblk0p3 179:3    0  64M  0 part /app
# └─mmcblk0p4 179:4    0 128M  0 part /data

# Test read/write on data partition
echo "test" > /data/test.txt
cat /data/test.txt
rm /data/test.txt
```

### 5. CPU and Power Management
- [ ] CPU frequency scaling works
- [ ] Governor configured correctly
- [ ] Thermal management functional
- [ ] Thermal zones detected
- [ ] CPU throttling works under load

```bash
# Check CPU frequency
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq

# Check governor
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor
# Should be: schedutil

# Check available frequencies
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_available_frequencies

# Check available governors
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_available_governors
# Should show: schedutil performance powersave

# Check thermal zones
ls /sys/class/thermal/thermal_zone*/
cat /sys/class/thermal/thermal_zone*/type
cat /sys/class/thermal/thermal_zone*/temp

# Monitor CPU frequency and temperature under load
watch -n 1 'paste <(cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq) <(cat /sys/class/thermal/thermal_zone0/temp)'

# Check thermal governor
cat /sys/class/thermal/thermal_zone0/policy
# Should be: step_wise

# Verify CPU cooling device
ls /sys/class/thermal/cooling_device*/
cat /sys/class/thermal/cooling_device0/type
```

### 6. tezsign Functionality Tests

#### Basic Connection Test
```bash
# On host machine
./tezsign list-devices
# Should show Radxa Zero 3 device
```

#### Initialization Test
```bash
# On host machine
./tezsign init
# Should prompt for password and complete successfully
```

#### Key Generation Test
```bash
# On host machine
./tezsign new consensus
# Should generate key successfully
```

#### Key Listing Test
```bash
# On host machine
./tezsign list
# Should show generated keys
```

#### Status Check
```bash
# On host machine
./tezsign status
# Should show device status and keys
```

#### Full Status with Proof of Possession
```bash
# On host machine
./tezsign status --full
# Should show BLpk and proof of possession
```

#### Key Unlock Test
```bash
# On host machine
./tezsign unlock consensus
# Should unlock key successfully
```

#### Signing Test
```bash
# On host machine
./tezsign run --listen 127.0.0.1:20090 &
# Should start signer server

# Test signing operation (requires registered keys)
# This would be done through your baker configuration
```

### 7. Hardware Platform Tests

#### DMA Engine
- [ ] DMA engine initialized
- [ ] DMA channels available

```bash
# Check DMA devices
ls -la /sys/class/dma/

# Check DMA engine
dmesg | grep -i dma | grep -i pl330

# Verify DMA is being used
cat /sys/kernel/debug/dmaengine/summary 2>/dev/null || echo "debugfs disabled (expected)"
```

#### USB PHY and Controller
- [ ] USB PHY initialized
- [ ] DWC3 controller detected
- [ ] USB role switching works

```bash
# Check USB PHY
ls -la /sys/class/phy/
dmesg | grep -i "usb.*phy"

# Check DWC3 controller
dmesg | grep -i dwc3
lsmod | grep dwc3

# Check USB role
cat /sys/class/usb_role/*/role 2>/dev/null || echo "Role switch interface not exposed (may be normal)"
```

#### RTC (Real-Time Clock)
- [ ] RTC detected
- [ ] Time can be read/set
- [ ] RTC persists across reboots

```bash
# Check RTC device
ls -la /dev/rtc*

# Read RTC time
hwclock -r

# Show RTC info
cat /sys/class/rtc/rtc0/name
cat /sys/class/rtc/rtc0/date
cat /sys/class/rtc/rtc0/time

# Check system time
date
```

#### Watchdog Timer
- [ ] Watchdog device present
- [ ] Watchdog can be started/stopped

```bash
# Check watchdog device
ls -la /dev/watchdog*

# Check watchdog info
cat /sys/class/watchdog/watchdog0/identity
cat /sys/class/watchdog/watchdog0/state
cat /sys/class/watchdog/watchdog0/timeout

# Check watchdog driver
dmesg | grep -i watchdog
```

#### GPIO and Pinctrl
- [ ] GPIO subsystem initialized
- [ ] Pinctrl configured

```bash
# Check GPIO chips
ls -la /sys/class/gpio/

# Check pinctrl
ls /sys/kernel/debug/pinctrl/ 2>/dev/null || echo "debugfs disabled (expected)"

# Verify GPIO driver loaded
dmesg | grep -i gpio | grep -i rockchip
```

#### I2C and SPI
- [ ] I2C buses detected
- [ ] SPI controllers initialized

```bash
# Check I2C buses
ls -la /dev/i2c-*
i2cdetect -l

# Check SPI devices
ls -la /dev/spidev* 2>/dev/null || echo "No SPI userspace devices (may be normal)"

# Verify drivers loaded
dmesg | grep -i "i2c.*rk3x"
dmesg | grep -i "spi.*rockchip"
```

### 8. System Services
- [ ] All tezsign services start correctly
- [ ] Services restart on failure
- [ ] Logging works correctly

```bash
# Check service status
systemctl status setup-gadget.service
systemctl status attach-gadget.service
systemctl status ffs_registrar.service
systemctl status tezsign.service

# Check service logs
journalctl -u setup-gadget.service
journalctl -u ffs_registrar.service
journalctl -u tezsign.service

# Verify no errors in logs
journalctl -b | grep -i error | grep -v 'DEQUEUE'
```

### 9. Security Verification
- [ ] Wireless drivers not loaded
- [ ] Bluetooth drivers not loaded
- [ ] Network stack disabled
- [ ] Unnecessary filesystems not available
- [ ] Debugging interfaces disabled
- [ ] Security hardening features enabled
- [ ] BPF disabled
- [ ] User namespaces disabled

```bash
# Check for network interfaces (should only show lo)
ip link show

# Check loaded modules for network/wireless
lsmod | grep -iE 'wifi|wlan|bluetooth|bt|net|eth'

# Should not show any network-related modules (except usb_f_ecm in dev mode)

# Check available filesystems
cat /proc/filesystems

# Should only show:
# ext4, ext2 (via ext4), vfat, tmpfs, sysfs, proc, configfs, devtmpfs

# Check for disabled features
zgrep CONFIG_WIRELESS /proc/config.gz
zgrep CONFIG_BT /proc/config.gz
zgrep CONFIG_SOUND /proc/config.gz
zgrep CONFIG_DRM /proc/config.gz
zgrep CONFIG_NET /proc/config.gz

# All should show "is not set"

# Verify security features are enabled
zgrep CONFIG_SECCOMP /proc/config.gz
zgrep CONFIG_STACKPROTECTOR /proc/config.gz
zgrep CONFIG_HARDENED_USERCOPY /proc/config.gz
zgrep CONFIG_FORTIFY_SOURCE /proc/config.gz
zgrep CONFIG_STRICT_KERNEL_RWX /proc/config.gz

# All should show "=y"

# Verify dangerous features are disabled
zgrep CONFIG_BPF_JIT /proc/config.gz
zgrep CONFIG_USER_NS /proc/config.gz
zgrep CONFIG_COREDUMP /proc/config.gz
zgrep CONFIG_KPROBES /proc/config.gz
zgrep CONFIG_FTRACE /proc/config.gz

# All should show "is not set"

# Check module loading restrictions
zgrep CONFIG_MODULE_UNLOAD /proc/config.gz
# Should show "is not set"

# Verify no debugfs
mount | grep debugfs
# Should show nothing

# Check for kernel symbols (should be minimal or none)
cat /proc/kallsyms | wc -l
# Should show very few symbols or none
```

### 10. Memory and Performance
- [ ] Memory usage is reasonable
- [ ] System responsive under load
- [ ] No memory leaks over time

```bash
# Check memory usage
free -h

# Check kernel memory
cat /proc/meminfo | head -20

# Check for OOM killer events
dmesg | grep -i oom

# Monitor system performance
top

# Expected: Low memory usage, minimal background processes
```

### 11. Long-term Stability Test
- [ ] Device runs for 24+ hours without issues
- [ ] Multiple sign operations work correctly
- [ ] No kernel warnings/errors over time
- [ ] Services remain stable

```bash
# Monitor system uptime
uptime

# Check for kernel errors over time
dmesg -T | grep -iE 'error|warning|fail'

# Monitor system logs
journalctl -f

# Check for file system errors
dmesg | grep -iE 'ext4|fat|filesystem'
```

## Dev Mode Testing

For dev images with ECM support:

### Network Interface Test
```bash
# Check for ECM interface
ip link show

# Should show tezsign_dev interface or similar

# Verify IP configuration on gadget side
ip addr show

# Expected: 10.10.10.1/24 on ECM interface
```

### SSH Access Test
```bash
# From host machine
ssh dev@10.10.10.1
# Password: tezsign

# Should connect successfully
```

## Troubleshooting

### Boot Issues

If device doesn't boot:
1. Connect serial console
2. Check U-Boot output
3. Verify SD card integrity
4. Check kernel panic messages

```bash
# Check boot log
dmesg | less

# Look for specific errors
dmesg | grep -iE 'panic|fatal|unable to mount'
```

### USB Gadget Issues

If USB gadget doesn't work:
```bash
# Check USB controller
lsmod | grep dwc3

# Check gadget configuration
cat /sys/kernel/config/usb_gadget/g1/UDC

# Check USB events
dmesg | grep -i usb

# Verify FunctionFS
ls -la /dev/ffs/tezsign/

# Check ffs_registrar service
systemctl status ffs_registrar.service
journalctl -u ffs_registrar.service
```

### Crypto Issues

If signing operations fail:
```bash
# Verify crypto algorithms
cat /proc/crypto | grep -A 5 sha256

# Check random number generation
cat /dev/random | head -c 32 | xxd

# Check hardware RNG
cat /sys/class/misc/hw_random/rng_available
cat /sys/class/misc/hw_random/rng_current
```

### Performance Issues

If system is slow:
```bash
# Check CPU frequency
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq

# Ensure governor is correct
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor

# Set performance governor if needed (testing only)
echo performance | sudo tee /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor

# Check for throttling
vcgencmd get_throttled  # if available
```

## Test Results Documentation

When submitting your test results, please include:

1. **Build Information**
   - Armbian version
   - Kernel version
   - Build date
   - Config fragment used

2. **Hardware Information**
   - Board model (Radxa Zero 3)
   - SD card brand/model
   - USB cable type

3. **Test Results**
   - All checklist items marked pass/fail
   - Any errors encountered
   - dmesg output (relevant portions)
   - lsmod output
   - df -h output
   - free -h output

4. **Performance Metrics**
   - Boot time
   - Memory usage at idle
   - Kernel size
   - Image size

5. **tezsign Functionality**
   - All tezsign operations tested
   - Any failures or issues
   - Performance observations

## Reporting Issues

If you encounter issues during testing:

1. Collect diagnostic information:
```bash
# Save system information
uname -a > sysinfo.txt
cat /proc/cpuinfo >> sysinfo.txt
cat /proc/meminfo >> sysinfo.txt
lsmod >> sysinfo.txt

# Save logs
dmesg > dmesg.log
journalctl -b > journal.log

# Save configuration
zcat /proc/config.gz > kernel.config
```

2. Document the issue:
   - Exact steps to reproduce
   - Expected behavior
   - Actual behavior
   - Any error messages

3. Submit via GitHub issue with:
   - Test results document
   - Diagnostic files
   - Issue description

## Success Criteria

A successful test means:
- ✅ All checklist items pass
- ✅ Device boots in < 60 seconds
- ✅ All tezsign operations work correctly
- ✅ No kernel errors or warnings
- ✅ System stable for 24+ hours
- ✅ Kernel size reduced by > 40%
- ✅ Memory usage < 150MB at idle
- ✅ No security features compromised

## Next Steps

After successful testing:
1. Document kernel size reduction achieved
2. Submit PR with test results
3. Include performance metrics
4. Note any optimizations made

Good luck with your testing!

