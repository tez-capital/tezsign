SUMMARY = "Raw files for the app partition"
LICENSE = "CLOSED"

DEPENDS += "go-native"

SRC_URI = " \
    file://.image-flavour \
    file://.image-version \
    file://.image-date \
"

inherit deploy externalsrc goarch

TEZSIGN_REPO_ROOT ?= "${@os.path.abspath(os.path.join(d.getVar('THISDIR'), '../../../..'))}"
EXTERNALSRC = "${TEZSIGN_REPO_ROOT}/app"
EXTERNALSRC_BUILD = "${WORKDIR}/build"

python () {
    import os

    repo_root = d.getVar("TEZSIGN_REPO_ROOT")
    go_mod = os.path.join(repo_root, "go.mod")
    gadget_dir = os.path.join(repo_root, "app", "gadget")
    if os.path.isfile(go_mod) and os.path.isdir(gadget_dir):
        return

    bb.fatal(
        "%s: TEZSIGN_REPO_ROOT=%s does not point at the tezsign repository root. "
        "When building inside a container, mount the repository root and export "
        "TEZSIGN_REPO_ROOT to that mounted path." % (d.getVar("PN"), repo_root)
    )
}

TEZSIGN_GADGET_GOARM64 = ""
TEZSIGN_GADGET_GOARM64:raspberrypi0-2w-tezsign = "v8.0"
TEZSIGN_GADGET_GOARM64:raspberrypi4-tezsign = "v8.0"
TEZSIGN_GADGET_GOARM64:radxa-zero3-tezsign = "v8.2"

TEZSIGN_GADGET_CGO_CFLAGS = ""
TEZSIGN_GADGET_CGO_CFLAGS:raspberrypi0-2w-tezsign = "-march=armv8-a -mcpu=cortex_a53 -D__BLST_PORTABLE__ -O2"
TEZSIGN_GADGET_CGO_CFLAGS:raspberrypi4-tezsign = "-march=armv8-a -mcpu=cortex_a72 -D__BLST_PORTABLE__ -O2"
TEZSIGN_GADGET_CGO_CFLAGS:radxa-zero3-tezsign = "-march=armv8.2-a+crypto -mcpu=cortex_a55 -O2"

do_configure() {
    :
}

do_compile[network] = "1"
do_compile[nostamp] = "1"
do_compile() {
    normalize_cpu_flags() {
        local out=""
        local flag val
        for flag in "$@"; do
            case "$flag" in
                -mcpu=*|-mtune=*)
                    val="${flag#*=}"
                    val="$(printf '%s' "$val" | tr '_' '-')"
                    flag="${flag%%=*}=${val}"
                    ;;
            esac
            out="${out} ${flag}"
        done
        printf '%s' "${out# }"
    }

    normalize_dynamic_ldflags() {
        local out=""
        local flag
        for flag in "$@"; do
            case "$flag" in
                -static|-static-pie|--static|-Wl,-static)
                    continue
                    ;;
            esac
            out="${out} ${flag}"
        done
        printf '%s' "${out# }"
    }

    install -d ${B} ${WORKDIR}/home ${WORKDIR}/go-cache ${WORKDIR}/go-mod-cache
    rm -rf ${WORKDIR}/go-cache/*

    export HOME="${WORKDIR}/home"
    export GOCACHE="${WORKDIR}/go-cache"
    export GOMODCACHE="${WORKDIR}/go-mod-cache"
    export GOOS="${TARGET_GOOS}"
    export GOARCH="${TARGET_GOARCH}"
    export CGO_ENABLED="1"
    cgo_cflags="$(normalize_cpu_flags ${CFLAGS} ${TEZSIGN_GADGET_CGO_CFLAGS})"
    cgo_cxxflags="$(normalize_cpu_flags ${CXXFLAGS} ${TEZSIGN_GADGET_CGO_CFLAGS})"
    cgo_ldflags="$(normalize_dynamic_ldflags ${LDFLAGS})"
    cgo_ldflags="${cgo_ldflags} -Wl,--build-id=none -Wl,-Bdynamic"
    go_ldflags="-s -w -buildid= -linkmode=external -extldflags '${cgo_ldflags}'"
    export CGO_CFLAGS="${cgo_cflags}"
    export CGO_CPPFLAGS="${CPPFLAGS}"
    export CGO_CXXFLAGS="${cgo_cxxflags}"
    export CGO_LDFLAGS="${cgo_ldflags}"

    if [ -n "${TEZSIGN_GADGET_GOARM64}" ]; then
        export GOARM64="${TEZSIGN_GADGET_GOARM64}"
    fi

    cd ${S}
    go build -a -v -trimpath -buildvcs=false \
        -ldflags="${go_ldflags}" \
        -o ${B}/tezsign \
        ./gadget

    if ! readelf -l ${B}/tezsign | grep -q 'Requesting program interpreter'; then
        bbfatal "tezsign must be dynamically linked (expected ELF interpreter)"
    fi

    ${STRIP} --strip-all ${B}/tezsign
}

do_install[noexec] = "1"
do_unpack[nostamp] = "1"
do_deploy[depends] += "virtual/${TARGET_PREFIX}binutils:do_populate_sysroot"

do_deploy() {
    install -d ${DEPLOYDIR}/appfs
    install -m 0755 ${B}/tezsign ${DEPLOYDIR}/appfs/tezsign

    # Prefer CI-provided metadata files; for local builds derive sane values.
    image_version="$(cat ${WORKDIR}/.image-version 2>/dev/null || true)"
    image_version="$(printf '%s' "$image_version" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    if [ -z "$image_version" ] || [ "$image_version" = "unknown" ]; then
        if [ -n "${IMAGE_VERSION}" ]; then
            image_version="${IMAGE_VERSION}"
        elif [ -n "${TEZSIGN_RELEASE_NAME}" ]; then
            image_version="${TEZSIGN_RELEASE_NAME}"
        elif [ -n "${PV}" ]; then
            image_version="${PV}"
        else
            image_version="unknown"
        fi
    fi

    image_date="$(cat ${WORKDIR}/.image-date 2>/dev/null || true)"
    image_date="$(printf '%s' "$image_date" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    if [ -z "$image_date" ] || [ "$image_date" = "unknown" ]; then
        if [ -n "${IMAGE_DATE}" ]; then
            image_date="${IMAGE_DATE}"
        elif [ -n "${SOURCE_DATE_EPOCH}" ]; then
            image_date="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)"
        fi
    fi
    if [ -z "$image_date" ] || [ "$image_date" = "unknown" ]; then
        image_date="unknown"
    fi

    image_flavour="$(cat ${WORKDIR}/.image-flavour 2>/dev/null || true)"
    image_flavour="$(printf '%s' "$image_flavour" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    if [ -z "$image_flavour" ] || [ "$image_flavour" = "unknown" ]; then
        # Manual builds: derive canonical flavour from kas-provided release name first, then machine.
        release_name="$(printf '%s' "${TEZSIGN_RELEASE_NAME}" | tr '[:upper:]' '[:lower:]')"
        machine_name="$(printf '%s' "${MACHINE}" | tr '[:upper:]' '[:lower:]')"

        case "$release_name" in
            rpi4|rpi4_dev)
                image_flavour="rpi4"
                ;;
            rpi0-2w|rpi0-2w_dev|rpi0_2w|rpi0_2w_dev)
                image_flavour="rpi0-2w"
                ;;
            radxa-zero3|radxa-zero3_dev|radxa_zero3|radxa_zero3_dev)
                image_flavour="radxa-zero3"
                ;;
        esac

        if [ -z "$image_flavour" ] || [ "$image_flavour" = "unknown" ]; then
            case "$machine_name" in
                raspberrypi4-tezsign*)
                    image_flavour="rpi4"
                    ;;
                raspberrypi0-2w-tezsign*)
                    image_flavour="rpi0-2w"
                    ;;
                radxa-zero3-tezsign*)
                    image_flavour="radxa-zero3"
                    ;;
            esac
        fi
    fi

    if [ -z "$image_flavour" ] || [ "$image_flavour" = "unknown" ]; then
        image_flavour="unknown"
    fi

    printf '%s\n' "$image_flavour" > ${DEPLOYDIR}/appfs/.image-flavour
    printf '%s\n' "$image_version" > ${DEPLOYDIR}/appfs/.image-version
    printf '%s\n' "$image_date" > ${DEPLOYDIR}/appfs/.image-date
    chmod 0444 ${DEPLOYDIR}/appfs/.image-flavour ${DEPLOYDIR}/appfs/.image-version ${DEPLOYDIR}/appfs/.image-date

    # Normalize the freshly built gadget binary before it lands in appfs.
    ${STRIP} --strip-all ${DEPLOYDIR}/appfs/tezsign
}

addtask deploy after do_compile before do_build
