SUMMARY = "Raw files for the app partition"
LICENSE = "CLOSED"

INHIBIT_DEFAULT_DEPS = "1"

SRC_URI = " \
    file://tezsign \
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

    printf '%s\n' "$image_version" > ${DEPLOYDIR}/appfs/.image-version
    printf '%s\n' "$image_date" > ${DEPLOYDIR}/appfs/.image-date
    chmod 0444 ${DEPLOYDIR}/appfs/.image-version ${DEPLOYDIR}/appfs/.image-date

    # Normalize the prebuilt gadget binary before it lands in appfs.
    ${STRIP} ${DEPLOYDIR}/appfs/tezsign
}

addtask deploy after do_compile before do_build
