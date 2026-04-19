SUMMARY = "Raw files for the app partition"
LICENSE = "CLOSED"

INHIBIT_DEFAULT_DEPS = "1"

SRC_URI = " \
    file://tezsign \
    file://.image-flavour \
    file://.image-version \
    file://.image-date \
"

inherit deploy

do_configure[noexec] = "1"
do_compile[noexec] = "1"
do_install[noexec] = "1"
do_deploy[depends] += "virtual/${TARGET_PREFIX}binutils:do_populate_sysroot"

do_deploy() {
    install -d ${DEPLOYDIR}/appfs
    install -m 0755 ${WORKDIR}/tezsign ${DEPLOYDIR}/appfs/tezsign

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

    # Normalize the prebuilt gadget binary before it lands in appfs.
    ${STRIP} ${DEPLOYDIR}/appfs/tezsign
}

addtask deploy after do_compile before do_build
