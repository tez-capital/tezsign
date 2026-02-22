#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  tools/builder/local_build_check.sh \
    [--imgs-dir <path>] \
    [--container-tag <docker-tag>] \
    [--skip-build] \
    [--no-dev-fallback] \
    [--sudo <auto|always|never>]

Description:
  Replays the GitHub Actions "build-gadget" matrix locally.
  It expects raw source images in the images directory:
    - raspberry_pi.img
    - raspberry_pi_dev.img (optional)
    - radxa_zero3.img
    - radxa_zero3_dev.img (optional)

  If *_dev.img files are missing, the script falls back to the matching
  non-dev source image by default, so the dev matrix rows can still run.
  Generated archives are written to: <imgs-dir>/archives
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

IMGS_DIR="${REPO_ROOT}/imgs"
ARCHIVES_DIR=""
CONTAINER_TAG="tezsign/builder:local"
SKIP_BUILD="false"
ALLOW_DEV_FALLBACK="true"
SUDO_MODE="auto"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --imgs-dir)
      IMGS_DIR="$2"
      shift 2
      ;;
    --container-tag)
      CONTAINER_TAG="$2"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD="true"
      shift 1
      ;;
    --no-dev-fallback)
      ALLOW_DEV_FALLBACK="false"
      shift 1
      ;;
    --sudo)
      SUDO_MODE="$2"
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

if [[ "$IMGS_DIR" != /* ]]; then
  IMGS_DIR="${REPO_ROOT}/${IMGS_DIR}"
fi
ARCHIVES_DIR="${IMGS_DIR}/archives"

case "$SUDO_MODE" in
  auto|always|never)
    ;;
  *)
    echo "Invalid --sudo value: $SUDO_MODE (expected auto|always|never)" >&2
    exit 1
    ;;
esac

require_cmd docker
require_cmd cp

mkdir -p "$IMGS_DIR" "$ARCHIVES_DIR"

DOCKER_PREFIX=()
case "$SUDO_MODE" in
  always)
    require_cmd sudo
    DOCKER_PREFIX=(sudo)
    ;;
  never)
    DOCKER_PREFIX=()
    ;;
  auto)
    if ! docker info >/dev/null 2>&1; then
      require_cmd sudo
      DOCKER_PREFIX=(sudo)
    fi
    ;;
esac

docker_cmd() {
  "${DOCKER_PREFIX[@]}" docker "$@"
}

resolve_source_image() {
  local source_artifact="$1"
  local image_id="$2"
  local source_img="${IMGS_DIR}/${source_artifact}.img"

  if [[ -f "$source_img" ]]; then
    printf '%s\n' "$source_img"
    return 0
  fi

  if [[ "$ALLOW_DEV_FALLBACK" == "true" && "$source_artifact" == *_dev ]]; then
    local fallback_img="${IMGS_DIR}/${image_id}.img"
    if [[ -f "$fallback_img" ]]; then
      echo "WARN: ${source_img} not found, using fallback ${fallback_img}" >&2
      printf '%s\n' "$fallback_img"
      return 0
    fi
  fi

  return 1
}

build_prerequisites() {
  echo "===> Building docker image for reconfiguration: ${CONTAINER_TAG}"
  docker_cmd build -f tools/Containerfile -t "$CONTAINER_TAG" "$REPO_ROOT"

  echo "===> Building tools/bin/builder"
  docker_cmd run --rm -e CGO_ENABLED=0 -v "${REPO_ROOT}:/work" "$CONTAINER_TAG" \
    go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
    -o ./tools/bin/builder ./tools/builder

  echo "===> Building gadget payload"
  docker_cmd run --rm -e GOOS=linux -e GOARCH=arm64 -e CGO_ENABLED=1 -v "${REPO_ROOT}:/work" "$CONTAINER_TAG" \
    go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
    -o ./tools/builder/assets/tezsign ./app/gadget

  echo "===> Building ffs_registrar payload"
  docker_cmd run --rm -e GOOS=linux -e GOARCH=arm64 -v "${REPO_ROOT}:/work" "$CONTAINER_TAG" \
    go build -buildvcs=false -ldflags='-s -w -extldflags "-static"' -trimpath \
    -o ./tools/builder/assets/ffs_registrar ./app/ffs_registrar
}

run_matrix_row() {
  local image_id="$1"
  local source_artifact="$2"
  local flavour="$3"

  local source_img
  if ! source_img="$(resolve_source_image "$source_artifact" "$image_id")"; then
    echo "Missing source image for matrix row: ${source_artifact}.img (image_id=${image_id}, flavour=${flavour})" >&2
    return 1
  fi

  local output_name="${image_id}.img.xz"
  local image_env="${image_id}"
  if [[ "$flavour" == "dev" ]]; then
    output_name="${image_id}.dev.img.xz"
    image_env="${image_id}.dev"
  fi

  local output_img="${ARCHIVES_DIR}/${output_name}"
  local source_container_img="/imgs/$(basename "$source_img")"
  local output_container_img="/imgs/archives/${output_name}"

  echo
  echo "===> Matrix row"
  echo "     image_id=${image_id}"
  echo "     source_artifact=${source_artifact}"
  echo "     flavour=${flavour}"
  echo "     source=${source_img}"
  echo "     output=${output_img}"

  docker_cmd run --rm --privileged -e IMAGE_ID="$image_env" -e CGO_ENABLED=0 \
    -v "${REPO_ROOT}:/work" \
    -v "${IMGS_DIR}:/imgs" \
    "$CONTAINER_TAG" \
    ./tools/bin/builder "$source_container_img" "$output_container_img" "$flavour"

  if [[ ! -s "$output_img" ]]; then
    echo "Expected output image missing or empty: $output_img" >&2
    return 1
  fi

}

main() {
  cd "$REPO_ROOT"

  if [[ "$SKIP_BUILD" != "true" ]]; then
    build_prerequisites
  else
    echo "===> Skipping build steps (--skip-build)"
  fi

  run_matrix_row "raspberry_pi" "raspberry_pi" "prod"
  run_matrix_row "raspberry_pi" "raspberry_pi_dev" "dev"
  run_matrix_row "radxa_zero3" "radxa_zero3" "prod"
  run_matrix_row "radxa_zero3" "radxa_zero3_dev" "dev"

  echo
  echo "===> Local build-gadget matrix completed."
  echo "Artifacts:"
  ls -lh \
    "${ARCHIVES_DIR}/raspberry_pi.img.xz" \
    "${ARCHIVES_DIR}/raspberry_pi.dev.img.xz" \
    "${ARCHIVES_DIR}/radxa_zero3.img.xz" \
    "${ARCHIVES_DIR}/radxa_zero3.dev.img.xz" \
    2>/dev/null || true
}

main "$@"
