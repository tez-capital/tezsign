podman run --privileged --rm -it \                                                                                     
    -v .:/work:Z \
    --userns=keep-id \
    --user v \
    -e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build kas-project.yml

podman run --privileged --rm -it \
    -v .:/work:Z \
    --userns=keep-id \
    --user v \
    -e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    shell -c "bitbake -c cleansstate linux-raspberrypi" kas-project.yml