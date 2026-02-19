#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  tools/builder/local_build_check.sh \
    --image-id <raspberry_pi|radxa_zero3> \
    [--source-img <path/to/source.img>] \
    [--source-url <direct-image-url>] \
    [--no-download] \
    [--flavour <prod|dev>] \
    [--output <path/to/output.img.xz>] \
    [--container-tag <docker-tag>]

Example:
  tools/builder/local_build_check.sh \
    --image-id raspberry_pi \
    --flavour prod \
    --output ./imgs/raspberry_pi.local.img.xz
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
}

is_valid_raw_image() {
  local img="$1"
  [[ -f "$img" ]] || return 1

  # Reject obviously tiny/non-image files early.
  local size_bytes
  size_bytes="$(stat -c%s "$img" 2>/dev/null || echo 0)"
  if (( size_bytes < 50 * 1024 * 1024 )); then
    return 1
  fi

  # diskfs expects a real partition table it can parse.
  if fdisk -l "$img" 2>/dev/null | grep -Eq 'Disklabel type:[[:space:]]+(dos|gpt)'; then
    return 0
  fi
  return 1
}

IMAGE_ID=""
SOURCE_IMG=""
SOURCE_URL=""
FLAVOUR="prod"
OUTPUT_IMG_XZ=""
CONTAINER_TAG="tezsign/builder:local"
AUTO_DOWNLOAD="true"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image-id)
      IMAGE_ID="$2"
      shift 2
      ;;
    --source-img)
      SOURCE_IMG="$2"
      shift 2
      ;;
    --source-url)
      SOURCE_URL="$2"
      shift 2
      ;;
    --no-download)
      AUTO_DOWNLOAD="false"
      shift 1
      ;;
    --flavour)
      FLAVOUR="$2"
      shift 2
      ;;
    --output)
      OUTPUT_IMG_XZ="$2"
      shift 2
      ;;
    --container-tag)
      CONTAINER_TAG="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$IMAGE_ID" ]]; then
  usage
  exit 1
fi

ROOTFS_TARGET_MB=0
case "$IMAGE_ID" in
  raspberry_pi*|raspberry-pi*)
    ROOTFS_TARGET_MB=1400
    ;;
  radxa_zero3*|radxa-zero3*)
    ROOTFS_TARGET_MB=2200
    ;;
  *)
    echo "No rootfs target mapping for image-id '$IMAGE_ID'." >&2
    exit 1
    ;;
esac

if [[ "$FLAVOUR" != "prod" && "$FLAVOUR" != "dev" ]]; then
  echo "Invalid --flavour: $FLAVOUR (expected prod|dev)" >&2
  exit 1
fi

if [[ -z "$SOURCE_IMG" ]]; then
  SOURCE_IMG="./imgs/${IMAGE_ID}.img"
fi

if [[ -z "$OUTPUT_IMG_XZ" ]]; then
  suffix=""
  if [[ "$FLAVOUR" == "dev" ]]; then
    suffix=".dev"
  fi
  OUTPUT_IMG_XZ="./imgs/${IMAGE_ID}.local${suffix}.img.xz"
fi

require_cmd docker
require_cmd fdisk
require_cmd dd
require_cmd xz
require_cmd curl
require_cmd resize2fs
require_cmd dumpe2fs
require_cmd e2fsck
require_cmd awk
require_cmd sort
require_cmd mktemp
require_cmd file

mkdir -p "$(dirname "$SOURCE_IMG")"
mkdir -p "$(dirname "$OUTPUT_IMG_XZ")"

if [[ ! -f "$SOURCE_IMG" && -f "${SOURCE_IMG}.xz" ]]; then
  echo "===> Found local compressed source image: ${SOURCE_IMG}.xz"
  xz -dc "${SOURCE_IMG}.xz" > "${SOURCE_IMG}.tmp"
  if is_valid_raw_image "${SOURCE_IMG}.tmp"; then
    mv "${SOURCE_IMG}.tmp" "$SOURCE_IMG"
  else
    rm -f "${SOURCE_IMG}.tmp"
    echo "Local compressed source image is not a valid raw disk image: ${SOURCE_IMG}.xz" >&2
  fi
fi

if [[ -f "$SOURCE_IMG" ]]; then
  if ! is_valid_raw_image "$SOURCE_IMG"; then
    echo "Existing source file is not a valid raw disk image (will re-download): $SOURCE_IMG" >&2
    file "$SOURCE_IMG" >&2 || true
    rm -f "$SOURCE_IMG"
  fi
fi

if [[ ! -f "$SOURCE_IMG" ]]; then
  if [[ "$AUTO_DOWNLOAD" != "true" ]]; then
    echo "Source image not found and auto-download disabled: $SOURCE_IMG" >&2
    exit 1
  fi

  urls=()
  if [[ -n "$SOURCE_URL" ]]; then
    urls=("$SOURCE_URL")
  else
    case "$IMAGE_ID" in
      raspberry_pi)
        # Workflow build matrix uses board=rpi4b, release=trixie, kernel_branch=legacy.
        urls=(
          "https://dl.armbian.com/rpi4b/Trixie_legacy_minimal"
          "https://dl.armbian.com/rpi4b/Trixie_current_minimal"
        )
        ;;
      radxa_zero3)
        # Workflow build matrix uses board=radxa-zero3, release=trixie, boot_scenario=binman-atf-mainline.
        urls=(
          "https://dl.armbian.com/radxa-zero3/Trixie_current_minimal"
          "https://dl.armbian.com/radxa-zero3/Trixie_vendor_minimal"
        )
        ;;
      *)
        echo "No default source URL mapping for image-id '$IMAGE_ID'. Use --source-url or --source-img." >&2
        exit 1
        ;;
    esac
  fi

  download_ok="false"
  for url in "${urls[@]}"; do
    echo "===> Downloading source image: $url"
    tmp_download="${SOURCE_IMG}.download"
    tmp_extract="${SOURCE_IMG}.tmp"
    rm -f "$tmp_download"
    rm -f "$tmp_extract"
    if ! curl -fL --retry 3 --retry-delay 2 -o "$tmp_download" "$url"; then
      echo "Download failed: $url" >&2
      continue
    fi

    if xz -t "$tmp_download" >/dev/null 2>&1; then
      echo "===> Extracting downloaded xz to: $SOURCE_IMG"
      xz -dc "$tmp_download" > "$tmp_extract"
      rm -f "$tmp_download"
      if is_valid_raw_image "$tmp_extract"; then
        mv "$tmp_extract" "$SOURCE_IMG"
        download_ok="true"
        break
      fi
      echo "Downloaded file from $url extracted but is not a valid raw disk image." >&2
      rm -f "$tmp_extract"
      continue
    fi

    if file "$tmp_download" | grep -qi 'HTML document'; then
      echo "Downloaded file from $url is HTML, skipping." >&2
      rm -f "$tmp_download"
      continue
    fi

    if file "$tmp_download" | grep -qi 'XZ compressed'; then
      echo "===> Extracting downloaded xz to: $SOURCE_IMG"
      xz -dc "$tmp_download" > "$tmp_extract"
      rm -f "$tmp_download"
      if is_valid_raw_image "$tmp_extract"; then
        mv "$tmp_extract" "$SOURCE_IMG"
        download_ok="true"
        break
      fi
      echo "Downloaded xz from $url extracted but is not a valid raw disk image." >&2
      rm -f "$tmp_extract"
      continue
    fi

    echo "===> Downloaded source is not xz, validating as raw image..."
    if is_valid_raw_image "$tmp_download"; then
      mv "$tmp_download" "$SOURCE_IMG"
      download_ok="true"
      break
    fi

    echo "Downloaded file from $url is not a valid raw disk image." >&2
    file "$tmp_download" >&2 || true
    rm -f "$tmp_download"
  done

  if [[ "$download_ok" != "true" || ! -f "$SOURCE_IMG" ]]; then
    echo "Failed to obtain source image: $SOURCE_IMG" >&2
    exit 1
  fi
fi

echo "===> Building local builder container: $CONTAINER_TAG"
docker build -f tools/Containerfile -t "$CONTAINER_TAG" .

echo "===> Building tools/bin/builder"
docker run --rm -e CGO_ENABLED=0 -v "$PWD":/work "$CONTAINER_TAG" \
  go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
  -o ./tools/bin/builder ./tools/builder

echo "===> Building gadget payload"
docker run --rm -e GOOS=linux -e GOARCH=arm64 -e CGO_ENABLED=1 -v "$PWD":/work "$CONTAINER_TAG" \
  go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
  -o ./tools/builder/assets/tezsign ./app/gadget

echo "===> Building registrar payload"
docker run --rm -e GOOS=linux -e GOARCH=arm64 -v "$PWD":/work "$CONTAINER_TAG" \
  go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
  -o ./tools/builder/assets/ffs_registrar ./app/ffs_registrar

echo "===> Building image with local builder"
docker run --rm --privileged -e IMAGE_ID="$IMAGE_ID" -v "$PWD":/work "$CONTAINER_TAG" \
  ./tools/bin/builder "$SOURCE_IMG" "$OUTPUT_IMG_XZ" "$FLAVOUR" --skip-wait

tmp_dir="$(mktemp -d /tmp/tezsign-local-build-XXXXXX)"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

raw_img="$tmp_dir/output.img"
rootfs_img="$tmp_dir/rootfs.img"

echo "===> Checking fixed rootfs size in built image"
xz -dc "$OUTPUT_IMG_XZ" > "$raw_img"

root_entry="$(
  fdisk -l "$raw_img" | awk -v img="$raw_img" '
    $1 ~ ("^" img "[0-9]+$") {
      start=""; end=""; sectors=""
      for (i=2; i<=NF; i++) {
        if ($i ~ /^[0-9]+$/) {
          if (start=="") { start=$i; continue }
          if (end=="") { end=$i; continue }
          if (sectors=="") { sectors=$i; break }
        }
      }
      if (sectors != "" && $0 ~ /Linux/) {
        print $1 " " start " " sectors
      }
    }
  ' | sort -k3,3nr | head -n1
)"

if [[ -z "$root_entry" ]]; then
  echo "Could not detect Linux rootfs partition in built image." >&2
  fdisk -l "$raw_img" >&2 || true
  exit 1
fi

read -r root_part_name root_part_start root_part_sectors <<< "$root_entry"
logical_sector_size="$(fdisk -l "$raw_img" | awk '/Sector size \(logical\/physical\):/ {for (i=1; i<=NF; i++) if ($i=="bytes") {print $(i-1); exit}}')"
if [[ -z "$logical_sector_size" ]]; then
  echo "Failed to determine logical sector size from image." >&2
  exit 1
fi

target_bytes=$((ROOTFS_TARGET_MB * 1024 * 1024))
if (( target_bytes % logical_sector_size != 0 )); then
  echo "Target rootfs size ${ROOTFS_TARGET_MB}MB is not aligned to logical sector size $logical_sector_size." >&2
  exit 1
fi

target_sectors=$((target_bytes / logical_sector_size))

echo "Partition: $root_part_name (start=$root_part_start sectors=$root_part_sectors)"
echo "Logical sector size: $logical_sector_size"
echo "Target sectors (${ROOTFS_TARGET_MB} MiB): $target_sectors"

if (( root_part_sectors != target_sectors )); then
  echo "FAIL: Rootfs partition size is ${root_part_sectors} sectors, expected ${target_sectors} sectors (${ROOTFS_TARGET_MB}MB)." >&2
  exit 2
fi

dd if="$raw_img" of="$rootfs_img" bs="$logical_sector_size" skip="$root_part_start" count="$root_part_sectors" status=none

e2fsck -pf "$rootfs_img" >/dev/null 2>&1 || true
min_blocks="$(resize2fs -P "$rootfs_img" | awk '{print $NF}' | tail -n1)"
block_size="$(dumpe2fs -h "$rootfs_img" 2>/dev/null | awk '/Block size:/ {print $3; exit}')"

if [[ -z "$min_blocks" || -z "$block_size" ]]; then
  echo "Failed to read ext4 min blocks / block size." >&2
  exit 1
fi

target_blocks=$((target_bytes / block_size))

min_bytes=$((min_blocks * block_size))
min_mib="$(awk -v b="$min_bytes" 'BEGIN { printf "%.2f", b/1024/1024 }')"

echo "Block size: $block_size"
echo "Minimum blocks: $min_blocks (~${min_mib} MiB)"
echo "Target blocks (${ROOTFS_TARGET_MB} MiB): $target_blocks"

if (( min_blocks <= target_blocks )); then
  echo "PASS: Built image satisfies ${ROOTFS_TARGET_MB}MB rootfs target."
else
  echo "FAIL: Built image does not satisfy ${ROOTFS_TARGET_MB}MB rootfs target." >&2
  exit 2
fi
