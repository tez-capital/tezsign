# FunctionFS Gadget

If you’re cross-compiling from x86_64 → arm64 u need these for blst:
```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
go build -v -trimpath -buildvcs=false -ldflags='-s -w -buildid= -extldflags "-static"' -o ./tools/builder/assets/tezsign ./app/gadget
```


For yocto you need it like this
```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
go build -v -trimpath -buildvcs=false -ldflags='-s -w -buildid= -extldflags "-static"' -o ./kas/meta-tezsign/recipes-core/images/files/tezsign ./app/gadget
```
