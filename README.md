# Web Relay

Web Relay is a single-ingress Go host for a Go transport plane and a Laravel
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

- `cmd/relayer` is the Go executable and lifecycle wiring.
- `cmd/bridge` is the transport-neutral Go edge plus the RoadRunner and HTTP
  runtime adapters (`webrelay`).
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
class implementing `Webong\WebRelay\Contracts\RoutePlanner`, or configure the
bundled registry planner and a `PathResolver`. Then choose one of these modes:

- `roadrunner` embeds RoadRunner in the Go process. It is the single-listener
  production path and uses RoadRunner's PHP worker transport.
- `http` keeps Laravel independently hosted by PHP-FPM, an HTTP server,
  Octane, or another Laravel-compatible host. Go remains the public edge and
  calls Laravel's planner route and proxies pass-through requests over HTTP.
- `standalone` starts only the Go transport host and is useful for diagnostics
  or adapter tests; it does not provide a Laravel control plane.

Set `WEB_RELAY_RUNTIME` to `roadrunner`, `http`, or `standalone`. For backward
compatibility, `ROADRUNNER_ENABLED=true` selects `roadrunner` when the new
variable is absent.

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

### RoadRunner configuration

Copy `.rr.yaml.example` to `.rr.yaml` and set the PHP worker command. Use the
Laravel worker executable (`vendor/bin/roadrunner-worker`), not
`artisan octane:start`; the latter starts a separate RoadRunner process and
would create a second front door. The configuration must enable the custom
middleware:

```yaml
http:
  middleware: ["web_relay", "gzip"]
```

Then start the embedded host from the repository root:

```bash
export WEB_RELAY_RUNTIME=roadrunner
export WEB_RELAY_INTERNAL_TOKEN='use-a-long-random-value'
export ROADRUNNER_CONFIG=.rr.yaml
go run ./cmd/relayer
```

`WEB_RELAY_INTERNAL_TOKEN` is required in embedded mode. The old
`ROADRUNNER_INTERNAL_TOKEN` name remains supported as a fallback. The token
is sent only on the PHP planner call and must match the Laravel configuration.
The planner path `/_internal/web-relay/plan` is reserved and cannot be called
as a public request through the Go middleware.

### HTTP Laravel backend

In `http` mode, start Laravel separately and point the Go host at its private
or local URL:

```bash
export WEB_RELAY_RUNTIME=http
export LARAVEL_BACKEND_URL=http://127.0.0.1:8000
export WEB_RELAY_INTERNAL_TOKEN='use-a-long-random-value'
go run ./cmd/relayer
```

Go sends `POST /_internal/web-relay/plan` to that backend and reverse-proxies
every `pass_through` request to it. The Go public listener still owns the
network edge, while Laravel owns application routing and the control plane.
The backend URL may contain a path prefix; the planner route is appended to
that prefix.

### Go configuration

- `WEB_RELAY_RUNTIME` - `roadrunner`, `http`, or `standalone` (default
  `standalone`; `ROADRUNNER_ENABLED=true` remains a legacy selector)
- `LARAVEL_BACKEND_URL` - Laravel base URL required by `http` mode
- `WEB_RELAY_INTERNAL_TOKEN` - required shared planner token; falls back to
  `ROADRUNNER_INTERNAL_TOKEN`
- `PORT` - Go host port (default `5001`)
- `ROADRUNNER_CONFIG` - RoadRunner YAML path (default `.rr.yaml`)
- `MAX_WORKERS` - asynchronous delivery workers (default `100`)
- `MAX_QUEUE_SIZE` - pending relay capacity (default `1000`)
- `REQUEST_TIMEOUT` - outbound request timeout (default `30s`)
- `MAX_BODY_SIZE` - ingress and planner response limit (default `10MB`)
- `MAX_IDLE_CONNS`, `MAX_CONNS_PER_HOST`, `IDLE_CONN_TIMEOUT` - HTTP pooling
- `SHUTDOWN_TIMEOUT` - standalone shutdown timeout (default `30s`)

`standalone` mode has no PHP planner wired by itself. Use `http` when Laravel
is hosted separately, or `roadrunner` when the Go process should own the
RoadRunner lifecycle.

## PHP control plane

The Laravel side can either implement
`Webong\WebRelay\Contracts\RoutePlanner` directly and configure it as
`WEB_RELAY_PLANNER`, or use the bundled `RegistryRoutePlanner` with a
`PathResolver` configured as `WEB_RELAY_PATH_RESOLVER`.

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
use Webong\WebRelay\Contracts\RoutePlanner;
use Webong\WebRelay\Protocol\Delivery;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\RoutePlan;

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
use Webong\WebRelay\Contracts\PathResolver;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\PathBinding;

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

## Selective subscriptions

Subscriptions may carry versioned, Laravel-shaped rules over the normalized
ingress document. Rules are evaluated by PHP before it returns a route plan,
so Go receives only the reply and relay destinations that matched.

The matching document contains `method`, `scheme`, `host`, `path`, `headers`,
`query`, and `body`. Header names are normalized to lowercase. A single header
value is a string; repeated values are a list. A JSON body is decoded once and
non-JSON bodies remain strings.

The PHP-facing DSL compiles to a normal `webong/web-proxy` destination:

```php
use Webong\WebRelay\Protocol\MatchRules;
use Webong\WebRelay\SubscribeEndpoint;
use Webong\WebRelay\SubscriptionDefinition;

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

The service provider registers `POST /_internal/web-relay/plan`. In
`roadrunner` mode, Go/RoadRunner intercepts that path before it can become a
public Laravel route. In `http` mode, the Go edge blocks the path publicly and
calls it only on the configured Laravel backend. The controller requires a
non-empty `WEB_RELAY_INTERNAL_TOKEN` matching the Go process; the old
`ROADRUNNER_INTERNAL_TOKEN` name remains accepted by the package config.

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

Set `WEB_RELAY_OCTANE_APP_PATH` when testing an application-owned Laravel host
instead of the fixture.
