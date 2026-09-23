# QuickJS-NG source

The C engine files in this directory are copied without modification from
[QuickJS-NG v0.15.1](https://github.com/quickjs-ng/quickjs/tree/v0.15.1), commit
`fd0a0210b7be00957751871e7e01b8291268fc29`. `LICENSE` is the upstream MIT
license. Gateway-owned `bridge.c`, `bridge.h`, and `qjs.go` expose only a
JSON-in/JSON-out operation to the Go router.

Upstream's CMake library target lists `quickjs.c`, `dtoa.c`, `libregexp.c`, and
`libunicode.c`; their directly included headers are vendored here. The
standard-library and OS modules are not included.
