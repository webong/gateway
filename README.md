# Gateway

Gateway is a single-ingress Go host for a Go transport plane and a Laravel
control plane. The host can embed RoadRunner or connect to an independently
served Laravel application over HTTP.

```text
public request
    -> Go edge (embedded RoadRunner or net/http)
    -> PHP planner (validation, registry, subscriber binding)
    -> route plan
       -> Go synchronous reply delivery
       -> Go asynchronous subscriber deliveries
       -> PHP pass-through for ordinary Laravel requests
```

The boundary is intentionally agnostic. A subscriber is a registry identity,
not a tenant, and the public path is supplied by the application. `/webhook`
is not special: a planner may bind `/provider/events/{opaque-value}` or any
other path it owns.

## Responsibilities

Go owns the public network edge, request capture, stable delivery IDs,
outbound HTTP connection pooling, DNS/SSRF checks, synchronous replies,
asynchronous delivery queues, and the selected host runtime lifecycle.

PHP/Laravel owns endpoint and subscription management through
`webong/web-proxy`, the public registry JSON API, request validation, matching,
subscriber resolution, and route planning. The bundled `RegistryRoutePlanner`
uses an application-provided `PathResolver` to read the registry, resolve
subscribers, and bind an arbitrary path. Applications may replace it with a
custom planner for provider-specific validation or handlers. Its migrations
stay on the PHP/application side.

Selective subscriber matching is also PHP-owned. Local PHP callers and remote
HTTP subscribers produce the same versioned Laravel-style JSON rules. PHP
evaluates them against the ingress request and returns only matched
destinations to Go; the Go transport never interprets or stores the DSL.

The Go service never exposes registry-management APIs, queries registry
storage, evaluates match rules, or runs PHP migrations. It handles ingress and
egress traffic and asks the PHP application for a route plan through the
selected runtime adapter.

## Repository layout

- `cmd/proxy` is the Go executable and lifecycle wiring.
- `cmd/bridge` is the transport-neutral Go edge and protocol boundary.
- `cmd/bridge/roadrunner` and `cmd/bridge/caddy` are the optional RoadRunner
  and FrankenPHP/Caddy runtime adapters.
- `cmd/internal` contains Go configuration, forwarding, worker, and logging
  implementation details.
- `src/` is the PHP/Laravel control plane and remains the Composer package
  source tree.
- `config/` contains the Laravel package configuration.
- `src/Servers`, `src/Reconciliation`, `database/migrations`, and
  `internal/provisioning` are the core managed-server control plane and
  runtime: shared storage, `/servers`, `/applications`, `/instances`, desired
  state, health, scaling, routing, reconciliation, and Local/Docker/Kubernetes
  drivers.
- `ext/reverb` adapts Laravel Reverb configuration and its dynamic application
  provider to the core server-type contracts.
- `ext/mercure` and `ext/centrifugo` adapt their server configuration and
  launch contracts to the same core runtime.

The root is intentionally a single Go module and Composer package. When the
PHP `vendor/` directory exists, use `-mod=mod` for Go commands because that
directory belongs to Composer, not Go.

## Choose a host runtime

Install the package dependencies in the Laravel application and configure a
class implementing `Webong\Gateway\Contracts\RoutePlanner`, or configure the
bundled registry planner and a `PathResolver`. Then choose one of these modes:

- `roadrunner` embeds RoadRunner in the Go process. It is the single-listener
  production path and uses RoadRunner's PHP worker transport. It can also
  start the sibling Go SMTP listener; SMTP planning uses the same embedded PHP
  worker through the in-process handler bridge.
- `http` keeps Laravel independently hosted by PHP-FPM, an HTTP server,
  Octane, or another Laravel-compatible host. Go remains the public edge and
  calls Laravel's planner routes and proxies pass-through requests over HTTP.
  This is the standard/Octane-compatible path and can also start SMTP.
- `standalone` starts only the Go transport host and is useful for diagnostics
  or adapter tests; it does not provide a Laravel control plane.

Set `GATEWAY_RUNTIME` to `roadrunner`, `http`, or `standalone`.

The `Planner` interface is the transport seam. Goridge is used by the embedded
RoadRunner adapter because it is RoadRunner's PHP worker transport; it is not
itself a complete PHP host or worker supervisor. Both HTTP requests and
session-oriented protocol events use that same embedded worker boundary, while
the standard non-RoadRunner deployment is `http` mode.

## Go transport plane

For every path, the Go edge:

1. captures the method, host, path, query, headers, and body;
2. calculates a stable SHA-256 delivery ID;
3. asks the PHP application for a `v1` route plan through the selected
   planner adapter;
4. performs the synchronous reply and/or queues the PHP-resolved relays.

In `roadrunner` mode, the planner call is an in-process RoadRunner HTTP-worker
dispatch. RoadRunner encodes the internal planning request onto its PHP worker
pool through Goridge; it does not open a second HTTP listener. In `http` mode,
the same JSON bridge is sent to the configured Laravel backend over ordinary
HTTP.

The JSON body fields are base64 encoded by both sides. This keeps the boundary
safe for non-UTF-8 payloads as well as JSON webhooks.

PHP normally returns only destination metadata in a plan. Go fills in the
original request body and headers when it materializes each delivery, avoiding
one additional copy of a large payload per subscriber.

## Gateway contract

The current HTTP bridge remains compatible with `RoutePlan v1`, but every HTTP
ingress now carries optional protocol metadata:

- `protocol=http`
- `event=request`
- `session_id` when a future long-lived adapter has a connection identity

The Go bridge also defines a protocol-neutral `GatewayEvent` and
`GatewayDecision` contract for session-oriented protocols. SMTP is implemented
through the maintained [`emersion/go-smtp`](https://github.com/emersion/go-smtp)
server library; the gateway supplies only its backend/session hooks. WebSocket
uses the same contract through the Go HTTP upgrade/session handler. Payloads are
base64 encoded, and
destinations use a typed protocol and target instead of assuming every
destination is an HTTP URL.

The planned ownership boundary is:

- Go owns sockets, TLS, protocol parsing, session lifecycle, streaming,
  backpressure, and outbound delivery.
- PHP owns authentication, validation, registry lookup, subscriber resolution,
  and protocol-specific route policy.

The existing HTTP planner remains on `RoutePlanner` for compatibility. SMTP
uses `ProtocolPlanner` and `GatewayDecision` without changing existing
Laravel `RoutePlanner` implementations.

### RoadRunner configuration

Copy `.rr.yaml.example` to `.rr.yaml` and set the PHP worker command. Use the
Laravel worker executable (`vendor/bin/roadrunner-worker`), not
`artisan octane:start`; the latter starts a separate RoadRunner process and
would create a second front door. The configuration must enable the custom
middleware:

```yaml
http:
  middleware: ["gateway", "gzip"]
```

The example keeps a fixed PHP pool warm and recycles individual workers after
`max_jobs` requests. Size `num_workers` from measured concurrent planner load;
avoid a dynamic allocator that scales the pool to zero when predictable
latency matters.

Then start the embedded host from the repository root:

```bash
export GATEWAY_RUNTIME=roadrunner
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
export ROADRUNNER_CONFIG=.rr.yaml
go run ./cmd/proxy
```

`GATEWAY_INTERNAL_TOKEN` is required in embedded mode. The token is sent
only on the PHP planner call and must match the Laravel configuration.
The planner path `/_internal/gateway/plan` is reserved and cannot be called
as a public request through the Go middleware.

To run SMTP alongside the embedded RoadRunner HTTP listener, add:

```bash
export GATEWAY_SMTP_ADDR=':2525'
export GATEWAY_SMTP_HOSTNAME='smtp.example.test'
go run ./cmd/proxy
```

The `gateway` middleware must remain enabled in `.rr.yaml`. Go waits until
RoadRunner has assembled that middleware around the PHP worker, then starts
SMTP with the same in-process PHP event planner. No second Laravel HTTP port
is opened.

### HTTP Laravel backend

In `http` mode, start Laravel separately and point the Go host at its private
or local URL:

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_LARAVEL_BACKEND_URL=http://127.0.0.1:8000
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
go run ./cmd/proxy
```

Go sends `POST /_internal/gateway/plan` to that backend and reverse-proxies
every `pass_through` request to it. The Go public listener still owns the
network edge, while Laravel owns application routing and the control plane.
The backend URL may contain a path prefix; the planner route is appended to
that prefix.

### SMTP listener

SMTP is available in `http` and `roadrunner` modes. `cmd/proxy` starts the Go
HTTP edge and SMTP listener as sibling listeners, while both use the same PHP
control-plane bridge. The SMTP protocol itself is provided by
[`emersion/go-smtp`](https://pkg.go.dev/github.com/emersion/go-smtp), including
ESMTP parsing, message framing, limits, graceful shutdown, and optional
STARTTLS support. The gateway adapter handles session metadata, calls PHP for
the decision, and queues the resolved HTTP subscriber deliveries.

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_LARAVEL_BACKEND_URL=http://127.0.0.1:8000
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
export GATEWAY_SMTP_ADDR=':2525'
export GATEWAY_SMTP_HOSTNAME='smtp.example.test'
go run ./cmd/proxy
```

Each accepted SMTP transaction is sent to
`POST /_internal/gateway/event` as a protocol-neutral event. PHP validates the
message, matches the agnostic endpoint/subscriber registry using the first
recipient as the route key, and returns `accept`, `reject`, or `deliver`. A
`deliver` decision is executed by Go through the normal outbound HTTP worker
pool. In `roadrunner` mode the event call stays in-process; in `http` mode it
uses the Laravel backend URL. Embedded Caddy/FrankenPHP deployments use the
`gateway_smtp` app below.

The current adapter does not advertise SMTP AUTH unless
`GATEWAY_SMTP_AUTH_ENABLED=true` is explicitly set. AUTH uses the
`authenticate` protocol event, so the Laravel application remains responsible
for validating the username and password. AUTH requires TLS and advertises
PLAIN only after a certificate/key pair is configured. STARTTLS is enabled by
providing the same certificate/key pair; set
`GATEWAY_SMTP_IMPLICIT_TLS=true` for SMTPS-style TLS from the first byte.

If an application supplies a custom `GATEWAY_PLANNER` without a
`GATEWAY_PATH_RESOLVER`, it should also configure a
`GATEWAY_PROTOCOL_PLANNER` implementing `ProtocolPlanner` for SMTP events.
Applications using the bundled registry planner can configure the resolver and
reuse the default protocol adapter.

### WebSocket transport

WebSocket upgrades use the public HTTP listener in `http` and `roadrunner`
runtime modes. Go performs the upgrade, owns the connection and message loop,
and sends `connect`, `message`, and `close` events to PHP. PHP can accept,
reject, respond on the socket, or resolve HTTP subscriber deliveries. The
same handler is mounted by the FrankenPHP/Caddy `gateway` module; no separate
WebSocket port or Laravel listener is required.

### Go configuration

- `GATEWAY_RUNTIME` - `roadrunner`, `http`, or `standalone` (default
  `standalone`)
- `GATEWAY_LARAVEL_BACKEND_URL` - Laravel base URL required by `http` mode
- `GATEWAY_INTERNAL_TOKEN` - required shared planner token
- `PORT` - Go host port (default `5001`)
- `ROADRUNNER_CONFIG` - RoadRunner YAML path (default `.rr.yaml`)
- `MAX_WORKERS` - asynchronous delivery workers (default `100`)
- `MAX_QUEUE_SIZE` - pending relay capacity (default `1000`)
- `REQUEST_TIMEOUT` - outbound request timeout (default `30s`)
- `MAX_BODY_SIZE` - ingress and planner response limit (default `10MB`)
- `MAX_IDLE_CONNS`, `MAX_CONNS_PER_HOST`, `IDLE_CONN_TIMEOUT` - HTTP pooling
- `SHUTDOWN_TIMEOUT` - standalone shutdown timeout (default `30s`)
- `GATEWAY_SMTP_ADDR` - enables the sibling Go SMTP listener in `http` or
  `roadrunner` mode
- `GATEWAY_SMTP_HOSTNAME` - SMTP greeting/domain (default `gateway.local`)
- `GATEWAY_SMTP_MAX_MESSAGE_SIZE` - accepted message limit (default `10MB`)
- `GATEWAY_SMTP_MAX_RECIPIENTS` - recipients per transaction (default `100`)
- `GATEWAY_SMTP_READ_TIMEOUT`, `GATEWAY_SMTP_WRITE_TIMEOUT` - SMTP socket
  timeouts
- `GATEWAY_SMTP_PLANNER_TIMEOUT` - PHP planning timeout (default `30s`)
- `GATEWAY_SMTP_TLS_CERT_FILE`, `GATEWAY_SMTP_TLS_KEY_FILE` - PEM certificate
  and private-key files for STARTTLS or implicit TLS
- `GATEWAY_SMTP_IMPLICIT_TLS` - use implicit TLS instead of plain SMTP with
  STARTTLS (default `false`)
- `GATEWAY_SMTP_AUTH_ENABLED` - require PHP-planned SMTP AUTH (default
  `false`; TLS is required)

PHP-side cache settings:

- `GATEWAY_CACHE_ENABLED` - enables registry route caching (default `true`)
- `GATEWAY_CACHE_STORE` - Laravel cache store; use a shared store such as
  Redis when multiple warm workers or application instances are running
- `GATEWAY_PATH_CACHE_TTL` - path-binding TTL in seconds (default `300`)
- `GATEWAY_ROUTE_CACHE_TTL` - endpoint/subscription snapshot TTL (default
  `30`)
- `GATEWAY_MISSING_CACHE_TTL` - negative lookup TTL (default `5`)
- `GATEWAY_CACHE_PREFIX` - cache-key prefix (default `gateway`)
- `GATEWAY_CACHE_CUSTOM_PROVIDERS` - opt custom `web-proxy` providers into
  route caching (default `false`)
- `GATEWAY_MUTATION_LOCK_STORE` - shared cache store used for conditional
  subscription mutations; defaults to `GATEWAY_CACHE_STORE`
- `GATEWAY_MUTATION_LOCK_SECONDS` - route lock lease (default `10`)
- `GATEWAY_MUTATION_LOCK_WAIT_SECONDS` - lock wait before `503` (default `5`)

`standalone` mode has no PHP planner wired by itself. Use `http` when Laravel
is hosted separately, or `roadrunner` when the Go process should own the
RoadRunner lifecycle.

## Performance benchmarks

Run the isolated PHP planner benchmark with a warm path/route cache and 100
subscriptions (half matching the request):

```bash
make benchmark-php
```

Change the workload without editing the test:

```bash
GATEWAY_BENCH_SUBSCRIBERS=500 \
GATEWAY_BENCH_ITERATIONS=1000 \
make benchmark-php
```

Estimate a starting warm-worker count for a measured target rate with 50%
headroom:

```bash
GATEWAY_BENCH_SUBSCRIBERS=500 \
GATEWAY_BENCH_TARGET_RPS=200 \
make benchmark-php
```

The estimate uses `ceil(target requests/sec × measured p95 seconds ×
headroom)`. Treat it as a starting point, then validate CPU, memory, queueing,
and tail latency on production-shaped infrastructure.

The benchmark reports mean, p50, p95, and p99 planner latency. It deliberately
does not impose a timing assertion because CI hardware is not a production
capacity target.

For end-to-end latency through the public Go ingress, prepare a route with the
subscriber count being tested, start the selected runtime, then run:

```bash
make benchmark ARGS='-url https://relay.example.test/provider/events/app-123 \
  -requests 5000 -concurrency 50 \
  -header "X-Event-Type: message.created"'
```

The load tool reports throughput, HTTP status counts, errors, and mean/p50/p95/
p99/max latency. Use non-matching rules to measure ingress plus PHP planning
without outbound network variance; use matching rules and a controlled sink to
measure the complete relay path.

The HTTP runtime smoke can run that ingress benchmark against its real Go →
Laravel → registry path before teardown:

```bash
GATEWAY_HTTP_BENCHMARK_REQUESTS=1000 \
GATEWAY_HTTP_BENCHMARK_CONCURRENCY=20 \
bash scripts/http-smoke.sh
```

## PHP control plane

The Laravel side can either implement
`Webong\Gateway\Contracts\RoutePlanner` directly and configure it as
`GATEWAY_PLANNER`, or use the bundled `RegistryRoutePlanner` with a
`PathResolver` configured as `GATEWAY_PATH_RESOLVER`.

The planner receives an `IngressRequest` containing the exact public path. It
should validate the request, resolve the agnostic endpoint/subscriber
registry, and return one of:

- `RoutePlan::passThrough()` for ordinary Laravel requests;
- `RoutePlan::respond(new Response(...))` for a PHP-generated response such as
  provider verification;
- `RoutePlan::relay(...)` with an optional synchronous reply and zero or more
  asynchronous `Delivery` objects.

With `RegistryRoutePlanner`, the resolver returns a `PathBinding` containing an
endpoint key, route scope/key, destination metadata criteria, and optional
channel. The planner then uses `webong/web-proxy` to resolve the endpoint and
its active destinations. A destination with
`metadata.delivery_mode=reply` becomes the single synchronous reply; all other
HTTP destinations become subscriber relays. PHP-native event and job
destinations are dispatched by `webong/web-proxy` in the planner process.
`subscriber_id` metadata is used when present; otherwise the destination owner
identifier is carried across the bridge.

### Custom route planner

```php
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;

final class ApplicationRoutePlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        // Match $request->path against the application's path registry.
        // Resolve the registered subscribers and validate the payload here.
        return RoutePlan::relay(
            relays: [new Delivery(
                url: 'https://subscriber.example.test/receive',
                subscriberId: 'subscriber-id',
            )],
        );
    }
}
```

### Application-owned paths

An application-owned path resolver can look up any path shape, for example
`/provider/events/{opaque-value}` or `/hooks/customer/abc`, without changing
the Go service:

```php
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;

final class ApplicationPathResolver implements PathResolver
{
    public function resolve(IngressRequest $request): ?PathBinding
    {
        $binding = $this->paths->match($request->method, $request->path);

        return $binding === null ? null : new PathBinding(
            endpointKey: $binding->endpointKey,
            scope: $binding->scope,
            key: $binding->routingKey,
            channel: $binding->channel,
            destinationMetadata: $binding->destinationMetadata,
        );
    }
}
```

The resolver is the application's route-binding seam. It can use Laravel
routes or a PHP-owned path registry; no Go migration or fixed `/webhook`
prefix is required.

### Registry route caching

The bundled planner caches two portable layers:

1. application path bindings (`path -> endpoint key`); and
2. database-backed `endpoint/scope/key -> active destinations` snapshots.

Provider objects, signing credentials, request bodies, and validation results
are never cached. Match rules still run for every ingress request, so cached
subscriptions cannot bypass request-specific matching.

Path caching is intentionally opt-in because an application resolver may use
headers or body fields. Implement `CacheablePathResolver` and return a key
containing every request attribute that can change its result:

```php
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Protocol\IngressRequest;

final class ApplicationPathResolver implements CacheablePathResolver
{
    public function cacheKey(IngressRequest $request): string
    {
        return implode('|', [$request->method, $request->host, $request->path]);
    }

    // resolve(...) remains the same as above.
}
```

`EndpointController` and `SubscribeEndpoint` rotate cache generations after
successful writes. If application code mutates `web-proxy` directly, invalidate
the affected snapshot explicitly:

```php
use Webong\Gateway\RegistryRouteCache;

app(RegistryRouteCache::class)->invalidateEndpoint($endpointKey);
app(RegistryRouteCache::class)->invalidatePaths(); // when path ownership changed
```

Generation keys make invalidation visible to every worker when the configured
Laravel cache store is shared. Cached entries have bounded TTLs and cache
backend failures fall through to the registry. Custom endpoint providers are
not cached by default because they may select destinations from payloads or
headers; opt them in only when `destinationsFor()` is stable for an endpoint,
scope, and route key.

## Selective subscriptions

Subscriptions may carry versioned, Laravel-shaped rules over the normalized
ingress document. Rules are evaluated by PHP before it returns a route plan,
so Go receives only the reply and relay destinations that matched.

The complete normative reference is
[`docs/subscription-dsl-v1.md`](docs/subscription-dsl-v1.md). Its
machine-readable JSON Schema is
[`docs/schemas/subscription-v1.schema.json`](docs/schemas/subscription-v1.schema.json).
Incremental `add`/`remove` operations are available through
`PATCH /registry/endpoints/{endpointKey}/subscriptions/{destinationId}/match`
and are described in the same reference. The registry API also supports
route-scoped listing, inspection, URL/metadata/type updates, pause/reactivate,
and recoverable removal. Mutations use ETag/`If-Match` revisions and a shared
route lock to prevent lost updates.

The matching document contains `method`, `scheme`, `host`, `path`, `headers`,
`query`, and `body`. Header names are normalized to lowercase. A single header
value is a string; repeated values are a list. A JSON body is decoded once and
non-JSON bodies remain strings.

The PHP-facing DSL compiles to a normal `webong/web-proxy` destination:

```php
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\SubscribeEndpoint;
use Webong\Gateway\SubscriptionDefinition;

$definition = SubscriptionDefinition::matching(
    subscriberId: 'workspace-42',
    subscriptionId: 'workspace-42-messages',
    webhookGroup: 'meta',
    routingScope: 'application',
    routingKey: 'app-123',
    url: 'https://subscriber.example.test/webhooks/meta',
    type: 'relay', // or reply for the route's single synchronous destination
    rules: [
        'headers.x-event-type' => ['required', 'in:message.created,message.updated'],
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ],
);

app(SubscribeEndpoint::class)->handle('meta-messenger', $definition);
```

No rules means match every request, preserving existing subscriptions.
Invalid persisted rules fail closed for that subscriber.

### Registry management over HTTP

PHP owns the standalone registry API. Set `REGISTRY_TOKEN`; both management
routes are public through the shared RoadRunner listener and require that
bearer token. Go only transports these ordinary HTTP requests to Laravel.

Create or ensure an endpoint through `webong/web-proxy`:

```bash
curl -X POST https://relay.example.test/registry/endpoints \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "client": "meta-messenger",
    "external_id": "app-123",
    "endpoint_key": "meta-messenger",
    "signing_secret": "provider-signing-secret",
    "verification_token": "provider-verification-token",
    "credential_owner_id": "workspace-42",
    "metadata": {"environment": "production"}
  }'
```

The response contains the registry's resolved `endpoint_key` and
`callback_url`. Use that resolved key when attaching a subscription with the
same canonical rule representation:

```bash
curl -X POST https://relay.example.test/registry/endpoints/RESOLVED_ENDPOINT_KEY/subscriptions \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "subscriber_id": "workspace-42",
    "subscription_id": "workspace-42-messages",
    "type": "relay",
    "webhook_group": "meta",
    "routing_scope": "application",
    "routing_key": "app-123",
    "url": "https://subscriber.example.test/webhooks/meta",
    "match": {
      "version": "v1",
      "rules": {
        "headers.x-event-type": ["required", "in:message.created,message.updated"],
        "body.account.id": ["required", "string", "in:account-42"]
      }
    }
  }'
```

The remote rule allowlist intentionally excludes database, file, custom-class,
and regular-expression rules. A failed rule skips that subscriber; it never
rejects the provider webhook. Both `relay` and `reply` subscriptions may carry
rules; only one synchronous reply may be registered for a routing scope/key.

The API is deliberately application-owned: the bridge does not assume a
tenant model, a `/webhook` prefix, or a Go-side registry schema. Endpoint and
destination storage are provided by the configured `webong/web-proxy`
registry; its Laravel migrations remain PHP-owned.

## Managed servers

Gateway core owns the complete managed-server control plane. Extensions only
register a server-technology provider and Go workload; core composes that with
the selected `local`, `docker`, or `kubernetes` runtime driver.

```text
POST   /servers
GET    /servers
GET    /servers/{server}
PATCH  /servers/{server}
DELETE /servers/{server}
POST   /servers/{server}/start
POST   /servers/{server}/stop
POST   /servers/{server}/restart
POST   /servers/{server}/scale
GET    /servers/{server}/health

POST   /applications
GET    /applications?server_id={server}
GET    /applications/{application}
PATCH  /applications/{application}
DELETE /applications/{application}

GET    /instances?server_id={server}
GET    /instances/{instance}
```

Application operations are capability-based. Core owns the encrypted record
and API, while the selected server-type provider validates, stores, redacts,
and presents its technology-specific credentials and configuration. A server
type without the application capability receives a clear `422` response.

Server responses include desired state and a core-derived health summary.
Scaling is available through the explicit `/scale` operation or a normal
server update; the technology provider validates constraints such as Mercure
Community's single-replica limit and Centrifugo's Redis requirement.

The configurable table environment variables remain
`GATEWAY_SERVERS_TABLE`, `GATEWAY_APPLICATIONS_TABLE`, and
`GATEWAY_INSTANCES_TABLE`. Their Laravel keys are under
`gateway.servers.tables`.

See [`ext/reverb`](ext/reverb), [`ext/mercure`](ext/mercure), and
[`ext/centrifugo`](ext/centrifugo) for technology-specific settings and
workload requirements.

## Internal planner routes

The service provider registers `POST /_internal/gateway/plan`. In
`roadrunner` mode, Go/RoadRunner intercepts that path before it can become a
public Laravel route. In `http` mode, the Go edge blocks the path publicly and
calls it only on the configured Laravel backend. The controller requires a
non-empty `GATEWAY_INTERNAL_TOKEN` matching the Go process.

The same provider registers `POST /_internal/gateway/event` for session-oriented
protocols such as SMTP. It uses the same internal token and is intended to be
reachable only from the Go listener or the loopback PHP bridge in the Caddy
deployment.

When embedding RoadRunner in Go, configure the PHP worker command as
`vendor/bin/roadrunner-worker` with `APP_BASE_PATH` pointing at the Laravel
application. Do not use `artisan octane:start` in the embedded configuration;
that command launches another RoadRunner server.

### Laravel Octane worker

Octane is supported as the Laravel worker runtime. Install it in the host
Laravel application, rather than making it a runtime requirement of this
package:

```bash
composer require laravel/octane spiral/roadrunner-cli spiral/roadrunner-http
```

The embedded host starts RoadRunner from Go; Octane's
`vendor/bin/roadrunner-worker` boots and resets Laravel inside the PHP worker.
Run `php artisan octane:install --server=roadrunner` only when you need Octane
to publish its application configuration. Do not run `php artisan
octane:start` for this topology.

### FrankenPHP/Caddy handler

The Go gateway can also be compiled as a Caddy HTTP handler and placed before
FrankenPHP's `php_server` handler. This keeps a single public FrankenPHP
listener while preserving the same ownership boundary: Go captures and
delivers network traffic, and Laravel remains the next handler for planning
and ordinary application requests.

The example configuration is in
`config/Caddyfile.frankenphp.example`. Copy it into the Laravel application's
FrankenPHP deployment and set `APP_PUBLIC_PATH`, `SERVER_NAME`, and
`GATEWAY_INTERNAL_TOKEN`.

Build a FrankenPHP binary with this module using the FrankenPHP builder image
or an equivalent `xcaddy` build. From a FrankenPHP source checkout or builder,
add the module path to its existing build command:

```bash
--with github.com/webong/gateway/cmd/bridge/caddy=/path/to/web-relay
```

For a custom FrankenPHP Docker image, add the same module to the builder's
`xcaddy build` command, then replace the runtime image's FrankenPHP binary.
The Caddy handler is compiled into the binary; it is not loaded dynamically at
runtime. The handler owns its Go delivery worker pool and drains it during a
Caddy configuration reload.

The same custom binary also contains the `gateway.smtp` Caddy app module. It
starts a sibling TCP listener from the FrankenPHP/Caddy process, but SMTP does
not pass through `php_server`; it calls the configured private `planner_url`
and then uses the same Go delivery pool. The example file binds that PHP bridge
to `127.0.0.1:8081` and configures it with `gateway_smtp` in the global block.
This provides the second deployment shape without duplicating the SMTP server
or protocol implementation. The app accepts `tls_cert_file`, `tls_key_file`,
`implicit_tls`, and `auth_enabled` options with the same semantics as the
`GATEWAY_SMTP_*` settings.

## Tests

Run the Go transport tests from the repository root:

```bash
go test -mod=mod ./...
```

Run Go vet with the same module-mode flag when the PHP Composer `vendor/`
directory exists at the repository root:

```bash
go vet -mod=mod ./...
```

Run the PHP test suite with the installed Testbench/Pest tooling:

```bash
vendor/bin/pest
```

Run the real Octane worker smoke test using the included minimal Laravel host:

```bash
bash scripts/octane-smoke.sh
```

Run the non-RoadRunner HTTP runtime smoke test. It starts Laravel with PHP's
built-in HTTP server, then starts Go as the public edge:

```bash
bash scripts/http-smoke.sh
```

Run the SMTP smoke test for both the HTTP Laravel backend and embedded
RoadRunner. It sends a real SMTP transaction through Go, the PHP protocol
planner, and the subscriber delivery worker:

```bash
bash scripts/smtp-smoke.sh
# or: make test-smtp
```

Run the WebSocket smoke test for both HTTP and RoadRunner. It upgrades a real
connection, sends a message through the PHP protocol planner, and verifies
the subscriber delivery:

```bash
bash scripts/websocket-smoke.sh
# or: make test-websocket
```

Verify the Caddy module and, when Docker is available, build a FrankenPHP
image containing both Gateway modules:

```bash
make test-frankenphp
GATEWAY_FRANKENPHP_BUILD=1 make test-frankenphp
```

Set `GATEWAY_OCTANE_APP_PATH` when testing an application-owned Laravel host
instead of the fixture.
