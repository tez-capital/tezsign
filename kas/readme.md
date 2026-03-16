Changes are done only in `meta-tezsign` (our yocto layer) and ymls.

1. needs tezsign and ffs_registrar in proper directories - see .gitignore

2. cd kas
3. build image:
```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user v \
    -e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build rpi4-dev.yml
```

NOTE: `-e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \` probably isnt necessary

To cleanup cache before rebuild (usually necessary after changes):

```sh
podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user v \
    -e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    shell -c "bitbake -c cleansstate minimal-image"  rpi4-dev.yml
```

Produced image is stored in the `release` (`kas/release`) directory.

