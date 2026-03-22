Changes are done only in `meta-tezsign` (our Yocto layer) and the KAS `.yml` files.

1. Put `tezsign` and `ffs_registrar` in the proper directories. See `.gitignore`.
2. `cd kas`
3. Build the image for the target board and mode you want.

Use this command for any Raspberry Pi build:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build <kas-file>
```

Available Raspberry Pi KAS files:

- `rpi4.yml`: Raspberry Pi 4 production image. Aggressively minimal and headless.
- `rpi4-dev.yml`: Raspberry Pi 4 dev image. Adds dev packages, HDMI debug overlay, and `console=tty1` for local monitor and keyboard debugging.
- `rpi0-2w.yml`: Raspberry Pi Zero 2 W production image. Aggressively minimal and headless.
- `rpi0-2w-dev.yml`: Raspberry Pi Zero 2 W dev image. Adds dev packages, local console, OTG mode, and display overlay for debugging.

Resolution chain:

- `rpi4-dev.yml` -> `rpi4.yml` -> `machine: raspberrypi4-tezsign`
- `rpi0-2w-dev.yml` -> `rpi0-2w.yml` -> `machine: raspberrypi0-2w-tezsign`

Typical commands:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi4.yml
```

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi4-dev.yml
```

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi0-2w.yml
```

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi0-2w-dev.yml
```

If you previously ran KAS with a different container user and now see errors like `detected dubious ownership` or `Cannot write to /work/build`, your `kas/` tree has mixed ownership. Clean the generated directories and rebuild:

```sh
rm -rf poky meta-raspberrypi meta-openembedded downloads build
```

To clean state before a Raspberry Pi rebuild:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    shell -c "bitbake -c cleansstate rpi-config rpi-bootfiles minimal-image" rpi4-dev.yml
```

Produced images are stored in `kas/release`.

Expected release names:

- `rpi4.yml` -> `rpi4.img`
- `rpi4-dev.yml` -> `rpi4_dev.img`
- `rpi0-2w.yml` -> `rpi0-2w.img`
- `rpi0-2w-dev.yml` -> `rpi0-2w_dev.img`
