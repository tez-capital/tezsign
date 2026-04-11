# FunctionFS Registrar

If you’re cross-compiling from x86_64 → arm64 u need these:

```bash
export GOOS=linux
export GOARCH=arm64
export CGO_ENABLED=0

go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" -o ./kas/meta-tezsign/recipes-core/tezsign-core/files/ffs_registrar ./app/ffs_registrar
```
