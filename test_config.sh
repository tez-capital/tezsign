#!/bin/bash
# Test build script for minimal kernel config

echo "🧪 Testing minimal kernel build..."
echo ""

# This would be run in armbian/build directory
# For now, just validate the config file

echo "📋 Validating config file..."

CONFIG_FILE="armbian_userpatches/kernel/radxa-zero3-minimal.config"

if [ ! -f "$CONFIG_FILE" ]; then
    echo "❌ Config file not found: $CONFIG_FILE"
    exit 1
fi

echo "✅ Config file exists"

echo ""
echo "📊 Config statistics:"
echo "  Total lines: $(wc -l < $CONFIG_FILE)"
echo "  Config options: $(grep -c '^CONFIG_' $CONFIG_FILE)"
echo "  Enabled (y): $(grep -c '=y$' $CONFIG_FILE)"
echo "  Disabled (n): $(grep -c '=n$' $CONFIG_FILE)"
echo "  Modules (m): $(grep -c '=m$' $CONFIG_FILE || echo 0)"

echo ""
echo "🔍 Checking critical options..."

CRITICAL_OPTIONS=(
    "CONFIG_USB_GADGET=y"
    "CONFIG_USB_DWC2=y"
    "CONFIG_USB_ETH=y"
    "CONFIG_CRYPTO=y"
    "CONFIG_EXT4_FS=y"
    "CONFIG_VFAT_FS=y"
    "CONFIG_SERIAL_8250_CONSOLE=y"
)

for opt in "${CRITICAL_OPTIONS[@]}"; do
    if grep -q "^$opt" "$CONFIG_FILE"; then
        echo "  ✅ $opt"
    else
        echo "  ❌ $opt MISSING"
    fi
done

echo ""
echo "📝 Build instructions:"
echo "  1. Copy this config to armbian/build/userpatches/kernel/"
echo "  2. Run: ./compile.sh BOARD=radxa-zero3 BRANCH=current"
echo "  3. Select this config when menuconfig appears"
echo "  4. Build and test"

echo ""
echo "✅ Config validation complete"
