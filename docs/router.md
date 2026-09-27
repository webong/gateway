# Gateway Router

Gateway Router bundles Spinner (Go) and Planner (PHP) into one service. The
included Planner has a thin Laravel bootstrap in `app/`, not a conventional
application skeleton or a separate service you must deploy. Choose a runtime:

| Runtime | Public listener | PHP execution |
| --- | --- | --- |
| RoadRunner | `ext/roadrunner/cmd/router` | Embedded PHP workers, no second listener |
| FrankenPHP library | Optional Go extension command | In-process FrankenPHP, no Caddy module |
| FrankenPHP/Caddy (legacy integration) | Custom FrankenPHP/Caddy binary with Gateway's Go module | In-process FrankenPHP |

All runtimes use the same Planner package, SQLite registry, `/hooks/{key}`
route convention, and management CLI. A container is optional.

The launcher chooses the Planner connection from the selected Router runtime.
`library` injects FrankenPHP's in-process handler; `roadrunner` uses its managed
PHP workers. Do not set `GATEWAY_RUNTIME` for `library`: that variable selects
the adapter only when running Spinner alone. The library Router also rejects
external Planner URL/socket settings, schedule polling, and provisioning,
which currently require the separate HTTP Planner topology.

The host uses `app/index.php`, `app/bootstrap.php`, and one `app/config.php`
array instead of Laravel's public/config/bootstrap directory skeleton.
Host configuration overrides framework defaults before providers register;
installed Composer packages are discovered normally and merge their own defaults.
The monorepo host also uses Composer Merge Plugin to load
`../ext/*/composer.json`. Those extension-owned manifests supply their
dependencies, autoload paths, and Planner companion registration without
adding extension names to Planner.
The current Proxy dependency uses the `proxy` configuration namespace.
For this integration test, both Composer manifests pin `webong/proxy` to
feature commit `b01cf9c` while the repository's `main` branch still declares
the former package name. Replace the temporary package repository entries with
the normal VCS repository after the Proxy rename reaches `main`.
Dependencies live in the shared root `vendor/` directory (`app/composer.json`
sets `vendor-dir` to `../vendor`). Use the app Composer manifest for a production
Router build; it shares a vendor location with the development manifest, so do
not alternate installs in a running deployment.

Laravel cache commands receive a generated compatibility bootstrap under the
data directory. No generated cache or storage files belong in `app/`.
Run `bash scripts/test-router-host` to check discovery, configuration caching,
and host routing using temporary runtime state.

## Build a native distribution

From the source checkout, install Planner dependencies and build a portable
directory for the current OS and architecture:

```bash
composer install --working-dir=app
./scripts/build-gateway-router
./dist/gateway-router/scripts/gateway-router roadrunner
```

The directory contains a prebuilt RoadRunner Router executable and the bundled Planner
with its PHP dependencies. Move it to a host with a compatible PHP runtime and
run the same launcher there; no Go toolchain, Composer, or source checkout is
required at runtime. Pass an output directory to `build-gateway-router` to
choose another location; it refuses to overwrite an existing one. The default
build contains the RoadRunner runtime. If you have a FrankenPHP binary built
with Gateway's Caddy module, set `GATEWAY_ROUTER_FRANKENPHP_BIN` while building
to include it as `bin/gateway-frankenphp`; then the same distribution also runs
with `scripts/gateway-router frankenphp`.
For the Go-library runtime, build `bin/gateway-router-library` using
`scripts/build-router-library`, then set `GATEWAY_ROUTER_LIBRARY_BIN` to that
binary while building the distribution. It requires Go 1.26 and a ZTS PHP
installation with the embed SAPI and matching `php-config` flags.

## Run from the source checkout

Install PHP 8.3+, Composer, and Go for the RoadRunner mode. The launcher builds
the RoadRunner Router from this checkout on startup unless `GATEWAY_ROUTER_ROADRUNNER_BIN` points
to a prebuilt executable.

```bash
composer install --working-dir=app
./scripts/gateway-router roadrunner
```

By default it listens on `127.0.0.1:8080` and keeps its generated credentials
SQLite database, PHP storage, and framework caches in `.gateway-router-data/`.
The `app/` tree stays free of runtime state. Set
`GATEWAY_ROUTER_LISTEN` and `GATEWAY_ROUTER_DATA_DIR` to change those paths;
keep the data directory across restarts and back it up as one unit. On first
start the launcher generates a Laravel key, an internal Spinner-to-Planner
credential, and a registry credential, then applies migrations.

An existing Router database created with `webong/web-proxy` needs the opt-in
Proxy legacy migration before its first start with this version. Back up the
Router data directory, load its usual database and app-key environment, and
run `app/artisan migrate --realpath` against
`vendor/webong/proxy/database/legacy-migrations` once. The normal startup then
applies the renamed Proxy migrations and creates the `profiles` table.

For the FrankenPHP Go-library extension, build and run the Router directly.
Spinner owns the listener, and the bundled Planner executes in the same process
without a second PHP listener or Caddy module:

```bash
./scripts/build-router-library
./scripts/gateway-router library
```

The legacy FrankenPHP/Caddy mode remains available as a custom integration,
but the Router Docker image uses the Go-library runtime above. The Caddy mode
requires a FrankenPHP binary built with the Gateway Caddy module; a stock
FrankenPHP binary does not contain Spinner.

```bash
GATEWAY_ROUTER_FRANKENPHP_BIN=/path/to/gateway-frankenphp \
  ./scripts/gateway-router frankenphp
```

The default native bind is loopback. Set `GATEWAY_ROUTER_LISTEN=:8080` only
when you intend to expose it behind your TLS/proxy layer. Set
`GATEWAY_PUBLIC_URL` to the published base URL when needed.

## Manage endpoints

In another shell, using the same data directory:

```bash
./scripts/gateway-router artisan gateway:endpoint orders
./scripts/gateway-router artisan gateway:subscribe orders orders-service https://receiver.example.com/hooks/orders
curl -X POST http://127.0.0.1:8080/hooks/orders \
  -H 'Content-Type: application/json' \
  --data '{"event":"created"}'
```

The CLI manages registry entries without asking you to copy a bearer token.
For the registry HTTP API, run
`./scripts/gateway-router artisan gateway:token` and use that token in an
Authorization header. Do not expose the private `/_internal/gateway/*`
endpoints. Callback destinations must pass Spinner's outbound address policy.

`GET /health` checks the combined listener and Planner. A webhook without a
subscriber can still be accepted; to verify actual delivery, configure a
controlled public HTTPS receiver.

## Optional Docker image

The image builds `ext/frankenphp/cmd/router`, which connects `lib/router` to the
`ext/frankenphp` PHP adapter, with Go 1.26 and bundles
the Planner on Debian. It compiles PHP 8.4 from the upstream release archive
with ZTS, the embed SAPI, and Linux execution timers, following FrankenPHP's
[library compilation requirements](https://frankenphp.dev/docs/library/).
The final image contains our Router binary, the matching PHP shared library
and CLI, and Planner dependencies. It uses no FrankenPHP base image or Caddy
binary. Optional FrankenPHP Brotli, file-watcher, and Mercure integration are
disabled with Go build tags.

Build and run the image:

```bash
docker build -f infra/docker/Gateway.router.Dockerfile -t gateway-router:local .
docker run -d --name gateway-router -p 8080:8080 \
  -v gateway-router-data:/data gateway-router:local
docker exec gateway-router /gateway/scripts/gateway-router artisan gateway:endpoint orders
```

The image ships the Router code, PHP dependencies, and empty writable cache
and storage directories under `/data`. It does not bake generated credentials,
the SQLite database, or Laravel cache files into the image; those are created
in the persistent volume on first start. `app/bootstrap.php` remains packaged
code, while the RoadRunner compatibility bootstrap is generated under the
data directory when that runtime is selected.

Keep the `/data` volume when replacing the container. The Go-only Spinner
remains available from the root `Dockerfile` for separate deployment with a
Laravel Planner over private HTTP. The existing FrankenPHP SMTP module has
its own configuration example in `infra/caddy/Caddyfile.frankenphp.example`.

For Kubernetes, use the separate [Router and Spinner manifests](kubernetes.md).

### RoadRunner Docker image

The alternative image packages `ext/roadrunner/cmd/router`, ordinary PHP CLI
workers, and the same Planner host. It does not use FrankenPHP, ZTS, or a
separate Planner service. It runs as `www-data`, listens on port 8080, and
persists credentials, SQLite, storage, and generated worker configuration in `/data`.

```sh
docker build -f infra/docker/RoadRunner.router.Dockerfile -t gateway-router:roadrunner-local .
docker run -d --name gateway-router -p 8080:8080 \
  -v gateway-router-data:/data gateway-router:roadrunner-local
docker exec gateway-router /gateway/scripts/gateway-router artisan gateway:endpoint orders
```

Choose either Router image for a deployment; do not run both against the same
SQLite volume concurrently. The existing single-replica Kubernetes Router
configuration can use this image by changing its image reference. The default
Docker image definition remains the FrankenPHP variant.

Run `bash scripts/test-roadrunner-container gateway-router:roadrunner-local`
to check health, non-root execution, private-route isolation, management CLI,
and restart persistence. The test deletes its own temporary container and volume.
