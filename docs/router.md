# Gateway Router

Gateway Router bundles Spinner (Go) and Planner (PHP) into one service. The
included Planner has a thin Laravel bootstrap in `app/`, not a conventional
application skeleton or a separate service you must deploy. Choose a runtime:

| Runtime | Public listener | PHP execution |
| --- | --- | --- |
| RoadRunner | `src/spinner/cmd/proxy` | Embedded PHP workers, no second listener |
| FrankenPHP | Custom FrankenPHP/Caddy binary with Gateway's Go module | In-process FrankenPHP |

Both runtimes use the same Planner package, SQLite registry, `/hooks/{key}`
route convention, and management CLI. A container is optional.

## Build a native distribution

From the source checkout, install Planner dependencies and build a portable
directory for the current OS and architecture:

```bash
composer install --working-dir=app
./scripts/build-gateway-router
./dist/gateway-router/scripts/gateway-router roadrunner
```

The directory contains a prebuilt Spinner executable and the bundled Planner
with its PHP dependencies. Move it to a host with a compatible PHP runtime and
run the same launcher there; no Go toolchain, Composer, or source checkout is
required at runtime. Pass an output directory to `build-gateway-router` to
choose another location; it refuses to overwrite an existing one. The default
build contains the RoadRunner runtime. If you have a FrankenPHP binary built
with Gateway's Caddy module, set `GATEWAY_ROUTER_FRANKENPHP_BIN` while building
to include it as `bin/gateway-frankenphp`; then the same distribution also runs
with `scripts/gateway-router frankenphp`.

## Run from the source checkout

Install PHP 8.3+, Composer, and Go for the RoadRunner mode. The launcher builds
Spinner from this checkout on startup unless `GATEWAY_ROUTER_SPINNER_BIN` points
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

For FrankenPHP, use a FrankenPHP binary built with the Gateway Caddy module
(the optional image below demonstrates the build). A stock FrankenPHP binary
does not contain Spinner.

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

The image currently packages the FrankenPHP runtime. It is one deployment
artifact for Router, not the definition of Router:

```bash
docker build -f docker/Gateway.router.Dockerfile -t gateway-router:local .
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
its own configuration example in `config/Caddyfile.frankenphp.example`.
