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

The root is intentionally a single Go module and Composer package. When the
PHP `vendor/` directory exists, use `-mod=mod` for Go commands because that
directory belongs to Composer, not Go.

## Choose a host runtime

Install the package dependencies in the Laravel application and configure a
class implementing `Webong\Gateway\Contracts\RoutePlanner`, or configure the
bundled registry planner and a `PathResolver`. Then choose one of these modes:

- `roadrunner` embeds RoadRunner in the Go process. It is the single-listener
  production path and uses RoadRunner's PHP worker transport.
- `http` keeps Laravel independently hosted by PHP-FPM, an HTTP server,
  Octane, or another Laravel-compatible host. Go remains the public edge and
  calls Laravel's planner route and proxies pass-through requests over HTTP.
- `standalone` starts only the Go transport host and is useful for diagnostics
  or adapter tests; it does not provide a Laravel control plane.

Set `GATEWAY_RUNTIME` to `roadrunner`, `http`, or `standalone`.

The `Planner` interface is the transport seam. Goridge is used by the embedded
RoadRunner adapter because it is RoadRunner's PHP worker transport; it is not
itself a complete PHP host or worker supervisor. A future direct-Goridge
adapter can plug into the same seam without changing the Go edge, while the
supported non-RoadRunner deployment today is `http` mode.

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
`GatewayDecision` contract for future WebSocket and SMTP adapters. Their
payloads are base64 encoded, and their destinations use a typed protocol and
target instead of assuming every destination is an HTTP URL.

The planned ownership boundary is:

- Go owns sockets, TLS, protocol parsing, session lifecycle, streaming,
  backpressure, and outbound delivery.
- PHP owns authentication, validation, registry lookup, subscriber resolution,
  and protocol-specific route policy.

The HTTP planner is intentionally not replaced yet. WebSocket and SMTP
adapters can adopt `ProtocolPlanner` and `GatewayDecision` without changing
existing Laravel `RoutePlanner` implementations.

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

## Internal planner route

The service provider registers `POST /_internal/gateway/plan`. In
`roadrunner` mode, Go/RoadRunner intercepts that path before it can become a
public Laravel route. In `http` mode, the Go edge blocks the path publicly and
calls it only on the configured Laravel backend. The controller requires a
non-empty `GATEWAY_INTERNAL_TOKEN` matching the Go process.

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

Set `GATEWAY_OCTANE_APP_PATH` when testing an application-owned Laravel host
instead of the fixture.
