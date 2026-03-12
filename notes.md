podman run --privileged --rm -it \                                                                                                                                                     2 ✘ │ 14:40:04 
    -v .:/work:Z \
    --userns=keep-id \
    --user v \
    -e KAS_PRE_QT_COMMAND="git config --global --add safe.directory '*'" \
    --workdir /work \
    ghcr.io/siemens/kas/kas:latest \
    build kas-project.yml