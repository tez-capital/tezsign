# FunctionFS Gadget

If you’re cross-compiling from x86_64 → arm64 u need these for blst:

```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
go build -v -trimpath -buildvcs=false -ldflags='-s -w -buildid= -extldflags "-static"' -o ./kas/meta-tezsign/recipes-core/images/files/tezsign ./app/gadget
```

rpi0-2w
```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
export GOARM64="v8.0"
export CGO_CFLAGS="-march=armv8-a -mtune=cortex_a53 -D__BLST_PORTABLE__ -O2"
go build -v -trimpath -buildvcs=false -ldflags='-s -w -extldflags "-static"'  -o ./kas/meta-tezsign/recipes-core/images/files/tezsign ./app/gadget
```

rpi4
```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
export GOARM64="v8.0"
export CGO_CFLAGS="-march=armv8-a -mtune=cortex_a72 -O2"
go build -v -trimpath -buildvcs=false -ldflags='-s -w -extldflags "-static"'  -o ./kas/meta-tezsign/recipes-core/images/files/tezsign ./app/gadget
```