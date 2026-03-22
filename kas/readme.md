Changes are done only in `meta-tezsign` (our Yocto layer) and the KAS `.yml` files.

1. Put `tezsign` and `ffs_registrar` in the proper directories. See `.gitignore`.
2. `cd kas`
3. Build the image for the target board you want.

RPi 4:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi4-dev.yml
```

RPi Zero 2 W:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi0-2w-dev.yml
```

The `-dev` files select the board-specific base file:
- `rpi4-dev.yml` -> `rpi4.yml` -> `machine: raspberrypi4-tezsign`
- `rpi0-2w-dev.yml` -> `rpi0-2w.yml` -> `machine: raspberrypi0-2w-tezsign`

If you previously ran KAS with a different container user and now see errors like `detected dubious ownership` or `Cannot write to /work/build`, your `kas/` tree has mixed ownership. Clean the generated directories and rebuild:

```sh
rm -rf poky meta-raspberrypi meta-openembedded downloads build
```

To clean state before rebuild:

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user "$(id -u):$(id -g)" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    shell -c "bitbake -c cleansstate minimal-image" rpi4-dev.yml
```

Produced images are stored in `kas/release`.
