# Gateway Router

Gateway Router bundles Spinner (Go) and Planner (PHP) into one service. The
included Planner has a thin Laravel bootstrap in `app/`, not a conventional
application skeleton or a separate service you must deploy. Choose a runtime:

| Runtime | Public listener | PHP execution |
| --- | --- | --- |
| RoadRunner | `pkg/roadrunner/cmd/router` | Embedded PHP workers, no second listener |
| FrankenPHP library | Optional Go extension command | In-process FrankenPHP, no Caddy module |
| FrankenPHP/Caddy (legacy integration) | Custom FrankenPHP/Caddy binary with Gateway's Go module | In-process FrankenPHP |

All runtimes use the same Planner package, database-backed registry, `/hooks/{key}`
route convention, and management CLI. A container is optional.

The launcher chooses the Planner connection from the selected Router runtime.
`library` injects FrankenPHP's in-process handler; `roadrunner` uses its managed
PHP workers. Do not set `GATEWAY_RUNTIME` for `library`: that variable selects
the adapter only when running Spinner alone. Both bundled runtimes can poll
scheduled automations through their in-process Planner; set
`GATEWAY_SCHEDULES_ENABLED=true` and install the desired Go language extension.
External Planner URL/socket settings and provisioning still require the
separate HTTP Planner topology.

The combined Router image, standalone Planner, and RoadRunner launcher use
`GATEWAY_PLANNER_WORKERS` as their deployment concurrency boundary. Bundled
Routers default to `1`; the standalone Planner accepts Octane's `auto` setting.
One persistent Planner worker keeps,
for each active dynamic package using `execution.mode: warmed`, at most one
warmed companion child in that worker. A value greater than `1` enables a PHP
worker pool. Each worker owns its own Laravel container and warmed companion,
so `GATEWAY_PLANNER_WORKERS=4` permits four companion invocations to run in
parallel without sharing child-process state. Planner still owns whether an
extension is `isolated` or `warmed`; the environment variable controls only
the deployment's maximum PHP concurrency. `GATEWAY_PLANNER_MAX_REQUESTS`
defaults to `500` and recycles each long-lived worker after bounded reuse. The
older `GATEWAY_ROUTER_PHP_WORKERS` name remains a compatibility fallback.

The host uses `app/index.php`, `app/bootstrap.php`, and one `app/config.php`
array instead of Laravel's public/config/bootstrap directory skeleton.
Host configuration overrides framework defaults before providers register;
installed Composer packages are discovered normally and merge their own defaults.
The monorepo host also uses Composer Merge Plugin to load
`../pkg/*/composer.json`. Those extension-owned manifests supply their
dependencies, autoload paths, and Planner companion registration without
adding extension names to Planner.
The Proxy dependency uses the `proxy` configuration namespace and resolves the
tagged `webong/proxy` release directly from Packagist.
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

By default it listens on `127.0.0.1:8080` and keeps its generated credentials,
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

All three bundled launchers also bind a provisioning-only Planner listener at
`127.0.0.1:5053`. Keep this loopback default for a colocated worker. For a
separate worker, set `GATEWAY_ROUTER_CONTROL_LISTEN` to a trusted private
interface and point its `GATEWAY_PLANNER_URL` to that origin. The
listener accepts only `/_internal/provisioning` paths; Planner applies the
worker-token or internal-token authorization. Never expose this port through
the public Router ingress, and enforce private-network access independently
of the token. The public listener continues to reject every `/_internal`
request.

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

The image builds `pkg/frankenphp/cmd/router`, which connects the
`pkg/spinner/router` host assembly to the `pkg/frankenphp` PHP adapter, with Go
1.26 and bundles
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

All Router launchers keep queued HTTP deliveries in `/data/deliveries` by
default. The legacy Caddy handler uses its `http/` child directory so it
cannot compete with the sibling SMTP app's spool worker. Preserve the spool
with the database when replacing a container. Caddy rejects an overlapping
hot reload of the same spool; restart that runtime to apply configuration
changes while durable delivery is enabled.
For multi-process delivery, set `GATEWAY_DELIVERY_DATABASE_URL` to a private
PostgreSQL URL instead of `GATEWAY_DELIVERY_SPOOL_PATH`. The bundled launcher
uses that same URL as Planner's `DB_URL`, migrates PostgreSQL, and selects the
shared-queue Caddyfile for FrankenPHP. Set `DB_CONNECTION=pgsql` explicitly if
your deployment supplies it; any non-PostgreSQL value is rejected. PHP must
have `pdo_pgsql` installed (both Router Docker images include it). The Go
workers claim only deliveries for adapters they support, allowing the HTTP
handler and SMTP app to share one database without misrouting work. See
[delivery operations](deliveries.md) for the at-least-once and remaining
cross-host limitations.
In this mode the bundled Planner also defaults Gateway's route cache and
subscription locks to PostgreSQL (`gateway_cache` and `gateway_cache_locks`).
The migration creates those tables. This makes subscription changes visible
to other Router replicas immediately, rather than waiting for each replica's
file-cache TTL. Set `GATEWAY_CACHE_STORE` and
`GATEWAY_MUTATION_LOCK_STORE` to another genuinely shared Laravel cache store
if desired; do not use separate file or array stores across replicas.
For multiple Router replicas, give each replica its own data directory and
mount the same trusted credentials file read-only. Set
`GATEWAY_ROUTER_CREDENTIALS_FILE` to that file's path. It must define the
same `APP_KEY`, `GATEWAY_INTERNAL_TOKEN`, and `REGISTRY_TOKEN` for every
replica and its management CLI. The launcher refuses a mounted file that
differs from an existing `gateway.env` in its data directory. This file is
sourced as shell input, so restrict who can edit or read it and keep it out
of the image and repository. Do not share a SQLite volume between replicas.
The legacy Caddy launcher binds the token-protected delivery admin listener
to `127.0.0.1:5052` by default and points its bundled Planner there. It
combines the handler and optional SMTP app spools without publishing the admin
port. See [delivery operations](deliveries.md) before overriding that address.

Keep the `/data` volume when replacing the container. The standalone Spinner
remains available from `infra/docker/Gateway.spinner.Dockerfile` for separate
deployment with a Laravel Planner over private HTTP. The existing FrankenPHP SMTP module has
its own configuration example in `infra/caddy/Caddyfile.frankenphp.example`.

For Kubernetes, use the separate [Router and Spinner manifests](kubernetes.md).

To route Reverb, Mercure, or Centrifugo instances created by a separate
provisioning worker, the FrankenPHP-library and RoadRunner Router images can
set `GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED=true` and a comma-separated
`GATEWAY_PROVISIONING_ROUTE_CIDRS` of reachable workload networks. The legacy
Caddy Gateway handler instead uses `managed_route_cidrs` in its `gateway`
block, as shown commented in `app/Caddyfile`. All three ask their bundled
Planner in-process for a current route and proxy matched HTTP/WebSocket
traffic to the reported instance. Keep the worker and Router on networks where
those instance IPs are reachable, and give workloads dedicated hostnames.
See [deployment targets](provisioning-targets.md#route-managed-workloads-from-a-separate-spinner)
for the routing and security constraints. The packaged RoadRunner and
FrankenPHP-library Routers have a multi-container managed HTTP/WebSocket smoke.
The optional FrankenPHP/Caddy integration can be tested against a separately
hosted Planner, but Gateway does not publish a dedicated image for it.

### RoadRunner Docker image

The alternative image packages `pkg/roadrunner/cmd/router`, ordinary PHP CLI
workers, and the same Planner host. It does not use FrankenPHP, ZTS, or a
separate Planner service. It runs as `www-data`; RoadRunner serves HTTPS on
8443 and keeps its HTTP health listener on container loopback. It persists
credentials, SQLite, delivery spool, storage, generated worker configuration,
and its local TLS certificate in `/data`.

```sh
docker build -f infra/docker/RoadRunner.router.Dockerfile -t gateway-router:roadrunner-local .
docker run -d --name gateway-router -p 8443:8443 \
  -v gateway-router-data:/data gateway-router:roadrunner-local
docker exec gateway-router /gateway/scripts/gateway-router artisan gateway:endpoint orders
```

On first start the container creates a self-signed certificate for
`GATEWAY_ROUTER_TLS_NAME` (default `gateway`) and `localhost`, stored with the
database in `/data/tls`. This is for local testing; mount a trusted certificate
and key with `GATEWAY_ROUTER_TLS_CERT_FILE` and `GATEWAY_ROUTER_TLS_KEY_FILE` for
deployed use. The same certificate is used for TLS IMAP unless separate IMAP
certificate paths are supplied. Copy only `/data/tls/server.crt` to local
clients that need to trust the generated certificate; never mount the entire
`/data` volume into a consuming application, because it holds Planner keys and
mail credentials. The management credential is generated in the private
`/data/gateway.env` file; retrieve it with the management CLI or provision a
private credentials file before startup.

For Planner-managed customer mailboxes, the bundle enables TLS IMAP on 993 and
a loopback-only SMTP credential resolver. Upstreams, accounts, mailbox
connections, runtime policy, and enablement are Planner resources managed by
API. An upstream's `network_scope` defaults to `public`; Spinner checks DNS
answers and pins the permitted IP at dial time. `private` must be selected
explicitly by an operator for an internal mail server. Loopback and link-local
addresses remain blocked. No exact-host environment list is needed. Do not
expose loopback ports 5052 or 5053. The private SMTP
resolver endpoint is available only on 5053 and still checks the internal
token; public HTTPS returns 404 for `/_internal` routes.

`bash scripts/test-router-mail-container gateway-router:roadrunner-local`
starts one disposable Router plus a test-only Dovecot mailbox and an SMTP
relay fixture. The relay must advertise 8BITMIME; the Mailpit image used in
the earlier local test did not, and Dovecot rejected `BODY=8BITMIME` when its
backend lacked support.
It verifies two isolated combined Planner connections, one IMAP-only
connection with no SMTP grant, a pending Gateway-managed sending domain,
TLS IMAP login, private SMTP credential lookup, HTTPS submission, idempotency,
and delivered receipts. Pass the local FrankenPHP-library image tag instead
to test that bundle. DNS ownership verification and managed sending require
a controlled TXT record and configured outbound relay; this smoke does not
prove those, compatibility with a real mailbox provider, or public-domain
mail delivery.

Choose either Router image for a deployment; do not run both against the same
SQLite volume concurrently. The existing single-replica Kubernetes Router
configuration can use this image by changing its image reference. The default
Docker image definition remains the neutral combined Router image.

Run `bash scripts/test-roadrunner-container gateway-router:roadrunner-local`
to check health, non-root execution, private-route isolation, management CLI,
and restart persistence. The test deletes its own temporary container and volume.
Run `bash scripts/test-router-managed-route gateway-router:roadrunner-local`
to check bundled Planner lookup, managed HTTP forwarding, a WebSocket upgrade
and echoed message, private-route isolation, destination allowlisting, and
stale-instance fail-closed behavior. This requires Docker and a local Go
toolchain. The same script accepts the FrankenPHP-library Router image. It
deletes its temporary containers, network, and volume.
The `scripts/test-caddy-managed-route` fixture remains available to consumers
that compile the optional Caddy module into their own host image. It verifies
that host against a separate Laravel Planner container, not the combined
Router image.
For the shared-delivery variant, build this image and run
`scripts/test-router-shared-postgres gateway-router:roadrunner-local`. That
disposable test runs two Router containers plus PostgreSQL and verifies
`pdo_pgsql`, Planner migrations, health, the private delivery-admin listener,
and a webhook created on one replica and routed through the other.
It does not prove failover across separate hosts.
