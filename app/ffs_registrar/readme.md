# FunctionFS Registrar

The readiness probe uses `bmRequestType=0xC1` (IN, vendor, interface),
`bRequest=0x5A`, and the interface number in `wIndex`. The registrar also accepts
the legacy `0x81` encoding for existing hosts. Upgrade the gadget image before
deploying a host that sends `0xC1`; older gadget images only accept `0x81`.

If you’re cross-compiling from x86_64 → arm64 u need these:

```bash
export GOOS=linux
export GOARCH=arm64
export CGO_ENABLED=0

go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o ./kas/meta-tezsign/recipes-core/tezsign-core/files/ffs_registrar ./app/ffs_registrar
```
