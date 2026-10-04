# Match the application module's Go version instead of downloading a toolchain at build time.
PV = "1.27.1"
SRC_URI[go_linux_amd64.sha256sum] = "63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"
SRC_URI[go_linux_arm64.sha256sum] = "3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec"
SRC_URI[go_linux_ppc64le.sha256sum] = "ad1ff83c24f783c2d4a025852b928a05c199e8742a14a5482d88de0293d2bd0d"
