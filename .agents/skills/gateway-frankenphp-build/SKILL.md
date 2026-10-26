---
name: gateway-frankenphp-build
description: Build or diagnose Gateway's FrankenPHP Router, especially CGO failures involving watcher-c, PHP headers, build tags, or differences between native and production Docker builds.
---

# Gateway FrankenPHP build

Treat [`infra/docker/Gateway.router.Dockerfile`](../../../infra/docker/Gateway.router.Dockerfile) as the canonical production build definition for both Router runtimes. Pass `--build-arg GATEWAY_ROUTER_RUNTIME=frankenphp` to select FrankenPHP; the default is RoadRunner. Read the selected build stage and tags before reproducing a failure; do not assume a plain `go test ./...` represents the shipped Router.

## Diagnose the intended target

1. Identify whether the request concerns the production Router image, the optional Caddy module, or an intentionally watcher-enabled FrankenPHP build.
2. For production Router verification, reproduce the Dockerfile build. Record the image identity and distinguish image-level verification from a native host build.
3. If a native command fails on `wtr/watcher-c.h`, compare its build tags with the Dockerfile. Gateway's production Router normally uses `nobrotli,nowatcher,nomercure`; retry with the current canonical tags before changing the host.
4. Native CGO verification must also use the PHP include and linker flags selected by the Dockerfile or an equivalent ZTS/embed PHP build. A host PHP installation is not proof of production compatibility.

## Choose the fix

- **Production Router:** preserve the canonical `nowatcher` build unless the product explicitly needs filesystem watching. Fix tests or scripts that accidentally compile the unused watcher path.
- **Optional Caddy integration:** run the repository's current Caddy/FrankenPHP test target and follow its module-specific dependencies.
- **Watcher-enabled build:** install and link the exact `github.com/e-dant/watcher` native library version required by the Go module, including `wtr/watcher-c.h` and `libwatcher-c`. Verify compiler include paths, linker paths, and runtime library discovery. Ask before installing system-wide packages or libraries.

Do not vendor generated native artifacts into the repository as a workaround. Keep source correctness, native-host prerequisites, production image verification, and published-image state separate in the report.

## Completion

Finish only when the requested build target succeeds or the remaining failure is isolated to a named external prerequisite. Report the exact command, tags, target, and whether the result was native or containerized.
