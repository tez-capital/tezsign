Changes are done only in `meta-tezsign` (our Yocto layer) and the KAS `.yml` files.

1. Put `tezsign` and `ffs_registrar` in the proper directories. See `.gitignore`.
2. `cd kas`
3. Build the image for the target board and mode you want.

Use this command for any Yocto build:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build <kas-file>
```

Available KAS files:

- `rpi4.yml`: Raspberry Pi 4 production image. Aggressively minimal and headless.
- `rpi4-dev.yml`: Raspberry Pi 4 dev image. Adds dev packages, HDMI debug overlay, and `console=tty1` for local monitor and keyboard debugging.
- `rpi0-2w.yml`: Raspberry Pi Zero 2 W production image. Aggressively minimal and headless.
- `rpi0-2w-dev.yml`: Raspberry Pi Zero 2 W dev image. Adds dev packages, local console, OTG mode, and display overlay for debugging.
- `radxa-zero3.yml`: Radxa Zero 3 production image. Headless production target mapped to the upstream `radxa-zero-3w` BSP machine.
- `radxa-zero3-dev.yml`: Radxa Zero 3 dev image. Adds dev packages, local console, and HDMI/USB keyboard kernel support.

Resolution chain:

- `rpi4-dev.yml` -> `rpi4.yml` -> `machine: raspberrypi4-tezsign`
- `rpi0-2w-dev.yml` -> `rpi0-2w.yml` -> `machine: raspberrypi0-2w-tezsign`
- `radxa-zero3-dev.yml` -> `radxa-zero3.yml` -> `machine: radxa-zero3-tezsign` -> upstream `radxa-zero-3w`
- `radxa-zero3.yml` -> `machine: radxa-zero3-tezsign` -> upstream `radxa-zero-3w`
- `radxa-zero3-dev.yml` -> `radxa-zero3.yml` -> `machine: radxa-zero3-tezsign` -> upstream `radxa-zero-3w`

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

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build radxa-zero3.yml
```

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build radxa-zero3-dev.yml
```

If you previously ran KAS with a different container user and now see errors like `detected dubious ownership` or `Cannot write to /work/build`, your `kas/` tree has mixed ownership. Clean the generated directories and rebuild:

```sh
rm -rf poky meta-raspberrypi meta-openembedded downloads build
```

To clean state before any rebuild:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    shell -c "bitbake -c cleansstate virtual/kernel minimal-image" <kas-file>
```

If cleaning image does not work.... try deleting the cache. This will mean redownloading everything.
```sh
sudo rm -rf poky meta-raspberrypi meta-openembedded downloads build
```


Produced images are stored in `kas/release`.

Expected release names:

- `rpi4.yml` -> `rpi4.img`
- `rpi4-dev.yml` -> `rpi4_dev.img`
- `rpi0-2w.yml` -> `rpi0-2w.img`
- `rpi0-2w-dev.yml` -> `rpi0-2w_dev.img`
- `radxa-zero3.yml` -> `radxa-zero3.img`
- `radxa-zero3-dev.yml` -> `radxa-zero3_dev.img`
