# FrankenPHP library extension

This optional Go module embeds the bundled PHP Planner in the Router process
through FrankenPHP's Go library. It imports no Spinner code and has no Gateway
core module dependency. `pkg/spinner/router` owns bundled host assembly; the
thin executable in `pkg/frankenphp/cmd/router` supplies this extension's PHP
handler to that package.
The executable has a separate Go module so its Spinner dependencies stay out
of runtime-adapter consumers. Native PHP dependencies remain outside the core
Spinner module. The command imports Spinner host assembly directly; the runtime
adapter does not.
The Docker and Kubernetes Router distributions use this same
extension; they do not change the standalone Spinner binary.

`infra/docker/Gateway.router.Dockerfile` builds `pkg/frankenphp/cmd/router` with the Go toolchain and
a PHP shared library compiled with ZTS and the embed SAPI. It packages the
result on Debian with the matching PHP CLI for Planner management commands.
It does not inherit a FrankenPHP application image. The container build disables
optional Brotli, file watching, and FrankenPHP's Mercure integration using
`nobrotli,nowatcher,nomercure`; the Router does not use those features.

The extension is pinned to FrankenPHP `v1.12.7`. Building it requires Go 1.26,
CGO, `php-config`, and PHP built with ZTS and the embed SAPI. From the repository
root, after installing the bundled Planner's Composer dependencies:

```sh
composer install --working-dir=app
./scripts/build-router-library
./scripts/gateway-router library
```

The build produces `bin/gateway-router-library`. The launcher creates local
Router credentials and SQLite state, runs migrations, then starts this binary.
The PHP Planner runs in-process; no PHP listener or Caddy module is required.
`GATEWAY_ROUTER_APP_DIR`, `GATEWAY_ROUTER_DATA_DIR`, and
`GATEWAY_ROUTER_LISTEN` control its paths and listener. The standalone Spinner
binary and the RoadRunner mode remain independent.
