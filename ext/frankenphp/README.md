# FrankenPHP library extension

This optional Go module embeds the bundled PHP Planner in the Router process
through FrankenPHP's Go library. It does not import the standalone Spinner
service command or add a FrankenPHP dependency to the core Go module. The
Router command composes the extension's PHP handler with Spinner's public HTTP
transport module. Docker and Kubernetes packaging are separate follow-up work.

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
