# Gateway

Gateway Router consists of **Spinner**, the Go transport module, and
**Planner**, the PHP control-plane package. They can be deployed separately or
together in the Gateway Router distribution.

| Distribution | What runs | Use it when |
| --- | --- | --- |
| Gateway Router | Spinner and the bundled Planner behind one public listener, hosted by embedded RoadRunner or FrankenPHP | You want a self-contained Gateway-owned registry |
| Spinner | Go image plus an external PHP Planner | Your Planner lives in an existing Laravel app or a separate service |
| Planner | `webong/gateway-planner` Composer package | Your Laravel app owns route policy and registry context |

The same route-plan and protocol-event contracts connect Spinner and Planner
in both deployment shapes. The optional `pkg/frankenphp` Go module embeds the
Planner through the FrankenPHP library without importing the standalone Spinner
command. The legacy Caddy adapter remains optional; the combined Router Docker
image uses the Go-library extension. RoadRunner embeds PHP workers in
`cmd/gateway`. None of these modes
needs a separate public Planner listener.

```text
public request
    -> Spinner (FrankenPHP library, FrankenPHP/Caddy, RoadRunner, or net/http)
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

For product setup and operations, see the [Gateway documentation](docs/README.md),
including [Gateway Router deployment](docs/router.md),
[distributed getting started](docs/getting-started.md), and
[deployment](docs/deployment.md).

## Responsibilities

Go owns the public network edge, request capture, stable delivery IDs,
outbound HTTP connection pooling, DNS/SSRF checks, synchronous replies,
asynchronous delivery queues, and the selected host runtime lifecycle.

PHP/Laravel owns endpoint and subscription management through
`webong/proxy`, the public registry JSON API, request validation, matching,
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

## Planner package distribution

The monorepo publishes separate Composer packages:

- `webong/gateway` contains the framework-free contracts and protocol values in
  `mod/adapter/src/`.
- `webong/gateway-planner` contains the Laravel Planner core in `pkg/planner/`.
- `webong/web`, `webong/dns`, `webong/email`, and `webong/database` are
  self-contained Gateway Extensions. Each owns its Planner policy entrypoint
  and Spinner transport entrypoint under one package identity and version.
- `webong/gateway-*` extension packages own their sources, dependencies, and
  discovery metadata. An extension may contribute one or more Integration,
  Automation, or Companion capabilities; those capability names describe what
  the package provides, while Extension describes the installable package.

The root development project and the bundled Router host use Composer Merge
Plugin to discover `pkg/*/composer.json`, so extension autoload paths and
providers come from the extension manifests rather than from duplicated root
mappings. The Go Spinner, Go Arranger library, bundled Router app, tests, and
deployment/build tooling are not part of the Composer packages.

`.gitattributes` controls GitHub/Git source archives; `archive.exclude` in
`composer.json` controls Composer-built archives for the core package. Verify
the core archive with `composer test-package` from a source checkout.

This applies to releases containing these packaging rules, not existing tags
or cached archives. `--prefer-source` (or a fallback to a Git clone) still
downloads the full repository. Use a Git clone for Router/Spinner builds:
the repository's generated source ZIP/tar downloads are core-only.
The Arranger release script constructs its own separate release tree and is
unaffected by these root archive rules.

## Repository layout

- `mod/arranger/integration` owns Integration traffic contracts, the installable
  Spinner process adapter, and runtime-neutral public/private HTTP routing.
  Extensions inject behavior without depending on Spinner or a PHP runtime.
- `pkg/spinner/router` assembles Spinner and an injected in-process Planner
  runtime for bundled hosts.
- `pkg/frankenphp/cmd/router` is the thin bundled executable in its own Go module.
- `pkg/roadrunner/runtime` manages PHP workers without Spinner imports;
  `pkg/roadrunner/cmd/router` assembles its bundled deployment through the
  shared Arranger routing policy and Spinner host components.
- `mod/arranger` is the independent `github.com/webong/gateway/arranger` Go
  module. Its automation, companion, and integration packages are the complete
  authoring surface for installable Gateway Extensions; its root package only
  documents that shared lifecycle. Extension authors can use it without Spinner;
  see the [integration and release guide](docs/integrations.md).
- `mod/adapter` is the framework-free PHP seam shared by Planner and its
  optional PHP companion packages; it contains interfaces and protocol values,
  never Planner implementations.
- Trusted extensions may register named [PHP companion actions](docs/companion-actions.md)
  in the Planner host and invoke them from Spinner through the Arranger
  companion interface; invocation transports data, never PHP source.
- `pkg/spinner` is the separate `github.com/webong/gateway/pkg/spinner` Go
  module; `pkg/spinner/proxy` contains its HTTP lifecycle package.
- `cmd/gateway` is the root-module composition executable; it installs optional
  Go extensions and starts Spinner.
- `pkg/spinner/bridge` is the transport-neutral Go edge and protocol boundary.
- `pkg/caddy` is the optional FrankenPHP/Caddy runtime adapter.
- `pkg/spinner/internal` contains Go configuration, forwarding, worker, and logging
  implementation details.
- `pkg/planner` is the PHP Planner core package source tree.
- `app/` is the thin PHP host for the bundled Planner, not a conventional
  Laravel application skeleton.
- `pkg/planner/config/` contains the Laravel Planner core configuration;
  `infra/caddy/` holds Caddy examples used by integration builds.
- `pkg/planner/src/Servers`, `pkg/planner/src/Reconciliation`, `pkg/planner/src/database/migrations`, and
  `pkg/spinner/provision` are the core managed-server control plane and
  runtime: shared storage, `/servers`, `/applications`, `/instances`, desired
  state, health, scaling, routing, reconciliation, and Local/Docker/Kubernetes
  drivers.
- `pkg/reverb` adapts Laravel Reverb configuration and its dynamic application
  provider to the core server-type contracts.
- `pkg/mercure` and `pkg/centrifugo` adapt their server configuration and
  launch contracts to the same core runtime.

Every Go runtime under `pkg/` owns its own module and runtime-specific
dependencies. Spinner is its own module and depends only on Arranger's public
contracts. The root `github.com/webong/gateway` module is the composition root
(`cmd/gateway`): it owns the JavaScript, Lua, and Shell module dependencies and
registers them through `cmd/gateway/composition`. Bundled RoadRunner and
FrankenPHP Router commands import that same composition package. Native
FrankenPHP remains outside Spinner-only builds. Arranger owns host-neutral
routing policy, while `pkg/spinner/router` owns bundled host assembly. The root
`go.work` ties these modules together for local commands; release builds use
each module directly.
When the PHP `vendor/` directory exists, use `-mod=mod` for Go commands because
that directory belongs to Composer, not Go.

## Choose a deployment

Run the combined distribution from [docs/router.md](docs/router.md). It
supplies a Planner application, SQLite persistence, migrations, and local
management commands. Choose embedded RoadRunner or FrankenPHP; the Docker
image is optional. No existing Laravel application is required.

Build the standalone Spinner image from
`infra/docker/Gateway.spinner.Dockerfile` when Planner is deployed separately.
Set `GATEWAY_PLANNER_URL` to its private HTTP
address. The standalone image contains no Planner application.

## Choose a Spinner host runtime

Install the package dependencies in the Laravel application and configure a
class implementing `Webong\Gateway\Contracts\RoutePlanner`, or configure the
bundled registry planner and a `PathResolver`. Then choose one of these modes:

- `http` keeps Laravel independently hosted by PHP-FPM, an HTTP server,
  Octane, or another Laravel-compatible host. Go remains the public edge and
  calls Laravel's planner routes and proxies pass-through requests over HTTP.
  This is the standard/Octane-compatible path and can also start SMTP.
- `diagnostic` starts only Go health and metrics listeners for adapter tests.

For the standalone Spinner executable, set `GATEWAY_RUNTIME` to `http` (the
default) or `diagnostic`. Bundled RoadRunner and FrankenPHP Routers select their
in-process Planner adapters in their extension commands and do not use
`GATEWAY_RUNTIME`.

The `Planner` interface is the transport seam. Goridge is used by the embedded
RoadRunner adapter because it is RoadRunner's PHP worker transport; it is not
itself a complete PHP host or worker supervisor. Both HTTP requests and
session-oriented protocol events use that same embedded worker boundary, while
the standard non-RoadRunner deployment is `http` mode.

## Go transport plane

For every path, the Go edge:

1. captures the method, host, path, query, headers, body, and active automation
   engine catalog;
2. calculates a stable SHA-256 delivery ID;
3. asks the PHP application for a `v1` route plan through the selected
   planner adapter;
4. performs the synchronous reply and/or queues the PHP-resolved relays.

In `roadrunner` mode, the planner call is an in-process RoadRunner HTTP-worker
dispatch. RoadRunner encodes the internal planning request onto its PHP worker
pool through Goridge; it does not open a second HTTP listener. In `http` mode,
the same JSON bridge is sent to the configured Planner over ordinary
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
`GatewayDecision` contract for non-HTTP protocols. SMTP is implemented
through the maintained [`emersion/go-smtp`](https://github.com/emersion/go-smtp)
server library; the gateway supplies only its backend/session hooks. WebSocket
uses the same contract through the Go HTTP upgrade/session handler. The DNS
adapter uses [`miekg/dns`](https://github.com/miekg/dns) as an authoritative
UDP/TCP server. Payloads are base64 encoded, and
destinations use a typed protocol and target instead of assuming every
destination is an HTTP URL.

The planned ownership boundary is:

- Go owns sockets, TLS, protocol parsing, session lifecycle, streaming,
  backpressure, and outbound delivery.
- PHP owns authentication, validation, registry lookup, subscriber resolution,
  and protocol-specific route policy.

The existing HTTP planner remains on `RoutePlanner` for compatibility. SMTP
and DNS use `ProtocolPlanner` and `GatewayDecision` without changing existing
Laravel `RoutePlanner` implementations.

### RoadRunner configuration

Copy `infra/roadrunner/Gateway.router.rr.yaml.example` to `.rr.yaml` and set the PHP worker command. Use the
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
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
export ROADRUNNER_CONFIG=.rr.yaml
go -C pkg/roadrunner/cmd/router run .
```

`GATEWAY_INTERNAL_TOKEN` is required in embedded mode. The token is sent
only on the PHP planner call and must match the Laravel configuration.
The planner path `/_internal/gateway/plan` is reserved and cannot be called
as a public request through the Go middleware.

To run SMTP alongside the embedded RoadRunner HTTP listener, install and
activate `webong/dns` and `webong/email` through Planner's `/extensions` API.
Put the SMTP listener under the Email Extension's `configuration.smtp` object;
do not set an SMTP listener environment variable on the generic Router host.
The `gateway` middleware must remain enabled in `.rr.yaml`. Spinner reconciles
the verified packages, starts DNS before Email, and Email uses the same
in-process PHP event planner without opening a second Laravel HTTP port. See
[Gateway Extension packages](docs/planner-extensions.md).

### HTTP Planner

In `http` mode, start Laravel separately and point the Go host at its private
or local URL:

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_PLANNER_URL=http://127.0.0.1:8000
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
go run ./cmd/gateway
```

Go sends `POST /_internal/gateway/plan` to that backend and reverse-proxies
every `pass_through` request to it. The Go public listener still owns the
network edge, while Laravel owns application routing and the control plane.
The Planner URL may contain a path prefix; the planner route is appended to
that prefix.

For a colocated Laravel process, retain the same HTTP contract but route it
over a Unix domain socket. The URL supplies the HTTP host and optional path
prefix; Go dials the socket instead of the URL's TCP host:

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_PLANNER_URL=http://gateway-planner
export GATEWAY_PLANNER_SOCKET=/run/gateway/planner.sock
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
go run ./cmd/gateway
```

Configure Nginx, Caddy, or another local HTTP server to listen on that socket.
This is an HTTP transport optimization for a single host, not a new PHP worker
or RPC runtime. Do not use a Unix socket when Gateway and Laravel are in
separate containers or nodes; use their private HTTP network address instead.

### SMTP listener

SMTP is available in `http` and bundled Router deployments through the
installable `webong/email` Spinner entrypoint. The generic Spinner reconciles
the package desired state from Planner; the Email child owns its listener while
both use the same PHP control-plane bridge. The SMTP protocol itself is provided by
[`emersion/go-smtp`](https://pkg.go.dev/github.com/emersion/go-smtp), including
ESMTP parsing, message framing, limits, graceful shutdown, and optional
STARTTLS support. The gateway adapter handles session metadata, calls PHP for
the decision, and queues the resolved HTTP subscriber deliveries.

Activate both `webong/dns` and `webong/email` with pinned HTTPS artifacts. An
Email runtime configuration can set `smtp.address`, `smtp.hostname`, TLS file
paths, authentication, recipient preflight, and bounded limits. Email declares
DNS resolution as an exact package capability dependency, so the host will not
start it when DNS is missing or incompatible. See the complete example in
[Planner Extensions](docs/planner-extensions.md).

Each accepted SMTP transaction is sent to
`POST /_internal/gateway/event` as a protocol-neutral event. PHP validates the
message, matches the agnostic endpoint/subscriber registry using the first
recipient as the route key, and returns `accept`, `reject`, or `deliver`. A
`deliver` decision is persisted by Go before the SMTP DATA command is
acknowledged. Set `GATEWAY_DELIVERY_SPOOL_PATH` when SMTP decisions may return
deliveries; without a durable queue Spinner returns a temporary SMTP error
instead of accepting a volatile handoff. In `roadrunner` mode the event call
stays in-process; in `http` mode it uses the Planner URL. Embedded
Caddy/FrankenPHP deployments reconcile the same `webong/email` package; the
Email child owns its protocol listeners and reaches Planner through the
generic in-process host contract. There are no protocol-specific Email
directives in the Caddy module.

The Email runtime does not advertise SMTP AUTH unless
`configuration.smtp.auth_enabled` is explicitly set. AUTH uses the
`authenticate` protocol event, so the Laravel application remains responsible
for validating the username and password. AUTH requires TLS and advertises
PLAIN only after a certificate/key pair is configured. STARTTLS is enabled by
providing the same certificate/key pair; set
`configuration.smtp.implicit_tls` for SMTPS-style TLS from the first byte.

The optional Email control plane manages TXT-verified domains, exact aliases,
`prefix*` patterns, and an optional catch-all alias at `/email/domains`. After a domain is enabled, Planner can return one
SMTP `submit` decision for all of its matched recipients. Spinner submits that
envelope to the configured outbound relay in one transaction and acknowledges
inbound DATA only after the relay accepts it. This is an early forwarding path;
see [mail forwarding limits](docs/mail-forwarding.md) before exposing it to
untrusted internet traffic.

If an application supplies a custom `GATEWAY_PLANNER` without a
`GATEWAY_PATH_RESOLVER`, it should also configure a
`GATEWAY_PROTOCOL_PLANNER` implementing `ProtocolPlanner` for SMTP events.
Applications using the bundled registry planner can configure the resolver and
reuse the default protocol adapter.

### DNS Extension

`webong/dns` is an installable Gateway Extension with a Planner entrypoint and
a Go Spinner entrypoint. Planner owns its CMS resources; Spinner receives one
revisioned, integrity-checked runtime snapshot through the Extension lifecycle.
Spinner core does not import DNS or read product behavior from DNS environment
variables.

DNS has three answer modes:

- **Managed:** exact records in a Planner-managed DNS Zone.
- **Provided:** a DNS Endpoint invokes one subscribed Webhook, Automation, or
  Integration and validates its synchronous answer document.
- **Forwarded:** a DNS Forwarder sends a matching query to an explicitly scoped
  upstream resolver.

DNS Zones and Forwarders are stored as versioned Planner Resources. DNS
Endpoints are generic routable endpoints because they own subscriptions. All
three modes write bounded `dns.query` observations to the shared event ledger.
There are no DNS-specific definition or history tables.

Create a managed zone:

```http
POST /dns/zones
Authorization: Bearer <registry-token>
Content-Type: application/json

{
  "name": "dns.example.com",
  "nameservers": ["ns1.example.net", "ns2.example.net"],
  "provisioner": "manual"
}
```

`manual` records desired delegation without changing external DNS. The optional
`cloudflare` provisioner creates child-zone NS records; its token remains a
deployment secret. `provisioner_options` may select `parent_zone_id` and
`parent_zone`. Apex registrar nameserver changes remain registrar-specific.

Create a Provided DNS Endpoint inside that zone:

```http
POST /dns/endpoints
Authorization: Bearer <registry-token>
Content-Type: application/json

{
  "external_id": "workspace-42-login-check",
  "zone": "dns.example.com",
  "name": "Login verification",
  "metadata": {"workspace": "workspace-42"}
}
```

The response contains an opaque token, its hostname, a
`{data}.<token>.<zone>` query template, an `endpoint_key`, and subscription,
history, and usage URLs. Attach its answer action through
`/registry/endpoints/{endpoint_key}/subscriptions`. A synchronous reply may
return bounded `A`, `AAAA`, `MX`, `CNAME`, or `TXT` records. Missing,
unavailable, or malformed answer providers fail closed.

Create a Forwarded policy with `POST /dns/forwarders`. Every policy requires an
upstream address, domain scope, and client CIDRs; unspecified catch-all client
networks are rejected so Gateway cannot accidentally become an open resolver.

The management surface is:

```text
GET    /dns/zones
POST   /dns/zones
GET    /dns/zones/{id-or-name}
PUT    /dns/zones/{id-or-name}/records
POST   /dns/zones/{id-or-name}/provision
DELETE /dns/zones/{id-or-name}

GET    /dns/endpoints
POST   /dns/endpoints
GET    /dns/endpoints/{id-or-token}
PATCH  /dns/endpoints/{id-or-token}
DELETE /dns/endpoints/{id-or-token}

GET    /dns/forwarders
POST   /dns/forwarders
GET    /dns/forwarders/{id}
PUT    /dns/forwarders/{id}
DELETE /dns/forwarders/{id}

GET    /dns/{zones|endpoints|forwarders}/{id}/events
GET    /dns/{zones|endpoints|forwarders}/{id}/usage
```

GET and mutation responses expose an `ETag`; replacement, provisioning, and
deletion require that revision in `If-Match`. This prevents two CMS clients
from silently overwriting one another. See [the DNS Extension](pkg/dns/README.md)
for runtime and answer-contract details.

### Spinner container

Build the Gateway router image with:

```bash
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t webong/gateway-spinner:local .
```

The image runs as an unprivileged `gateway` user and exposes the Go router's
HTTP (`5001`), optional SMTP (`2525`), and unprivileged DNS (`5353` TCP/UDP)
listeners. It intentionally does not contain a Laravel application: deploy the
Gateway Laravel planner as its companion application and point the router at it
with `GATEWAY_PLANNER_URL`, or mount a colocated planner socket using
`GATEWAY_PLANNER_SOCKET`.

Run the image-only smoke test (build, start, health probe, and non-root check)
when Docker is available:

```bash
make test-container
```

`make test-container-integration` is the deployment-level smoke test. It
builds a Laravel planner fixture and the Go router in separate containers,
leaves the planner on the private Compose network, and puts Caddy with a local
test certificate in front of the public router. It verifies TLS access,
private planner-route isolation, registry bearer authentication, public
subscription creation, route planning, and that a private subscriber address
is rejected by the outbound SSRF policy.

### Production DNS deployment

The Spinner image exposes HTTP and reserves unprivileged TCP/UDP port 5353 for
the installable DNS Extension. The included
`infra/compose/Gateway.spinner-dns.compose.yml` publishes both DNS transports
on public port 53:

```bash
GATEWAY_PLANNER_URL=https://app.internal.example \
GATEWAY_INTERNAL_TOKEN=replace-me \
docker compose -f infra/compose/Gateway.spinner-dns.compose.yml up -d
```

Configure `REGISTRY_TOKEN`, run the Gateway and WebProxy migrations on Planner,
then install and enable `webong/dns` through `POST /extensions` with
`configuration.address` set to `:5353`. Create each domain through
`POST /dns/zones`; Planner projects active zones and Forwarders into the
Extension desired state. Spinner independently pulls the verified package and
restarts only that Extension process when the projected snapshot changes. At
each parent DNS provider, delegate
the zone to at least two public nameserver hostnames whose A/AAAA records reach
Gateway instances. Each load balancer or firewall must pass both UDP and TCP
port 53. The example is a single-node wiring reference; production high
availability requires multiple Gateway nodes, shared registry/cache storage,
health checks at the infrastructure layer, and nameservers placed in independent
failure domains.

### WebSocket transport

WebSocket upgrades use the public HTTP listener in `http` and `roadrunner`
runtime modes. Go performs the upgrade, owns the connection and message loop,
and sends `connect`, `message`, and `close` events to PHP. PHP can accept,
reject, respond on the socket, or resolve HTTP subscriber deliveries. The
same handler is mounted by the FrankenPHP/Caddy `gateway` module; no separate
WebSocket port or Laravel listener is required.

Each Spinner/Router process admits at most 1000 WebSocket sessions by default,
including sessions awaiting a Planner connection decision. Excess upgrades
receive HTTP 503 with `Retry-After: 1`. A session closes after 24 hours by
default, and individual messages remain limited by the host's `MAX_BODY_SIZE`.
Set `configuration.websocket.max_connections` and
`configuration.websocket.max_duration_ms` on the revisioned `webong/web`
resource. The selected Web process receives that snapshot regardless of the
generic host. Limits apply per Extension process, not across a cluster.

### Webstream transport

Webstream is Gateway's one-way Web capability. A client opens a `GET` or `POST`
request with `Accept: text/event-stream` under a Gateway route; Spinner sends a
`webstream`/`connect` event to Planner. Planner applies the same path binding,
registry matching, and authorization policy used for Gateway routes. A matched
Proxy **reply** destination is the single upstream SSE source, not a webhook
reply: Planner returns an explicit `stream` decision and Spinner proxies its
bytes to the client without buffering them.

The upstream must return HTTP 200 with `text/event-stream`, or HTTP 204 to tell
the client to stop reconnecting. Redirects are not followed: configure the final
source URL so credentials cannot be forwarded to a different destination. Spinner
flushes each read to the client, blocks on slow client writes (so backpressure
propagates), and uses the client request context for the upstream request, so a
disconnect cancels the source connection. Endpoint-owned Proxy profile
transport settings remain attached to the source. The request timeout bounds
opening the source through receipt of response headers, not the established
stream. Streaming clears the HTTP server's connection-wide write deadline and
uses a 30-second deadline per write/flush to disconnect stalled readers.

For a protected source, attach an endpoint-scoped Proxy profile to the reply
subscription. Its encrypted `options.headers` are sent only with the selected
Webstream source request (for example, `Authorization: Bearer …`); they are
not exposed through registry resources, copied from the client, or used for
ordinary relay deliveries. Source profile headers override a same-named client
header case-insensitively; connection-framing headers and `Last-Event-ID` are reserved, so the
resume marker always remains client-controlled.

By default POST Webstreams accept a buffered request body followed by an SSE
response. Spinner reads the body before planning, under
the configured maximum body size and a 15-second read deadline. Oversized bodies
(including chunked requests) receive 413. `Content-Encoding: gzip` is supported;
both compressed and decoded bytes must fit the body limit. Malformed gzip is
rejected with 400; other or stacked encodings receive 415. Planner and source
receive decoded bytes, with encoding/length and stale digest headers removed.
Signatures over compressed wire bytes cannot be verified against this decoded
payload; use uncompressed requests for wire-body signature authentication.
Admission also counts requests still uploading.
Planner sees the method, headers, query, and body; the selected source must
permit POST. Registry planning preserves the body and content type unless the
selected delivery overrides them, without copying client credentials or cookies.
Custom planners must explicitly return `stream.attributes.method = "POST"` and
the approved `stream.payload`; a missing method retains the GET default.

By default POST requests are sent once, without redirects, automatic replay, or an injected
`retry:` hint. A disconnected POST may already have caused upstream work: only
the application can decide whether it is safe to resubmit. An event ID is not an
idempotency guarantee. Use `fetch()` or another streaming HTTP client, not native
`EventSource`, for POST. For example (the response chunks still need SSE parsing):

```javascript
const abortController = new AbortController();
const response = await fetch('/hooks/updates', {
  method: 'POST',
  headers: { Accept: 'text/event-stream', 'Content-Type': 'application/json' },
  body: JSON.stringify({ input: 'hello' }),
  signal: abortController.signal,
});
if (!response.ok) throw new Error(`Stream failed: ${response.status}`);
if (response.status !== 204) {
  const reader = response.body.getReader();
  // Consume incrementally with an SSE parser; chunks are not event boundaries.
}
```

#### Opt-in replay and continuous uploads

Create an exact public host/path policy through Planner. It pins the selected
source URL; normal registry authorization still applies:

```sh
curl -X POST https://planner.example.test/webstream/routes \
  -H "Authorization: Bearer $REGISTRY_TOKEN" -H 'Content-Type: application/json' \
  -d '{"host":"gateway.example.test","path":"/hooks/updates","source":"https://producer.example.test/events","retry":true,"upload":true}'
```

`GET`, `POST`, `GET /{id}`, `PUT /{id}`, and `DELETE /{id}` are available under
`/webstream/routes`. Replacing or disabling a policy takes effect on the next
Planner decision. A disabled or absent Planner policy does not enable upload or
retries.
`POST` and `GET /{id}` return an ETag such as `"1"`. Send that value as
`If-Match` for `PUT` or `DELETE`; missing or stale revisions are rejected.

Buffered POST retries require one nonempty `Idempotency-Key` (up to 255 bytes).
Spinner makes at most one extra attempt, after 100ms, with the same approved
body and key, within the original opening deadline. Only transient connection
failures before any response byte qualify: HTTP errors, redirects, cancellation,
and failures after response streaming starts are never retried. A source profile
cannot replace the key. The producer—not Gateway—owns deduplication and retention.

Clients request continuous uploads with `X-Gateway-Upload-Mode: stream` on POST.
Planner receives an empty payload plus `attributes.upload_mode = stream` and
must authorize from route/header metadata only. Do not enable this on routes
requiring full-body matching, signatures, or validation. Without route opt-in,
the request is rejected before reading the body or contacting the source.
Spinner streams the body directly, with backpressure and no full-body buffering,
and may return SSE while upload is in progress. Gzip is decoded incrementally.
Wire/decoded size limits still apply, with a 15-second read-idle deadline and a
five-minute upload deadline. Streamed uploads never retry, even on retry-enabled
routes, and cannot use a configured replacement body. A late upload error closes
the source/response; it cannot change an HTTP status already sent, nor undo bytes
the producer has consumed. Browsers and intermediaries may buffer uploads or
require HTTP/2; validate the actual deployment and client before relying on duplex.

Custom planners opt in through `stream.attributes.upload_mode = stream` with an
empty payload, or `stream.attributes.retry_idempotent = true` with an approved
`Idempotency-Key` header, never both. The standard PHP registry planner enforces
the Planner-owned policy above. The continuous-upload opening phase has a
five-minute cap; ordinary buffered opens retain the normal request timeout.

The Web Extension blocks private SSE source addresses by default, even when the
source was selected by Planner. First set a narrow
`configuration.webstream.private_network_cidrs` ceiling on the revisioned
`webong/web` resource, then create an exact-origin source grant through the
authenticated Planner API:

```sh
curl -X POST https://planner.example.test/webstream/private-sources \
  -H "Authorization: Bearer $REGISTRY_TOKEN" -H 'Content-Type: application/json' \
  -d '{"origin":"http://events.internal:8080","cidrs":["10.42.7.19/32"]}'
```

`GET`, `POST`, `GET /{id}`, `PUT /{id}`, and `DELETE /{id}` are available under
`/webstream/private-sources`; `PUT` replaces the exact origin, CIDRs, and
optional `enabled` flag. A grant takes effect on the next Planner decision
without a Spinner restart. Spinner accepts the grant only inside its deployed
network ceiling. The grant applies only to Webstream, never ordinary webhook
forwarding. Both layers require private RFC 1918 or IPv6 unique-local CIDRs;
Spinner checks the resolved address again when dialing, disallows a source
profile proxy, and does not follow redirects. Keep both ranges narrow.
`POST` and `GET /{id}` return an ETag; send it as `If-Match` for `PUT` or
`DELETE` to prevent overwriting a newer grant.

Both the network ceiling and the exact-origin grant are Planner-owned desired
state. The ceiling alone cannot grant a private source, and generic Gateway
hosts do not read a Webstream environment variable.

For GET, Spinner emits `retry: 3000` by default so conforming SSE clients reconnect. On
reconnect, `Last-Event-ID` is supplied to Planner as `last_event_id` and the
built-in registry planner forwards it only to the selected source. It emits a
comment heartbeat every 15 seconds by default when the source is between SSE
events, never inside an incomplete event. Configure
`configuration.webstream.retry_ms` and `heartbeat_ms` on `webong/web` (set the
heartbeat to `0` to disable it). `/metrics` exposes
`webstreams_active`, `webstreams_opened_total`, and `webstreams_failed_total`
in the standalone Spinner runtime.

Each Spinner/Router process accepts at most 1000 concurrent Webstreams by
default, counting streams being planned or opened. Further attempts receive
HTTP 503 with `Retry-After: 1` before Planner or the upstream is called.
Streams close after 24 hours by default so clients reconnect with their
`Last-Event-ID` if the source supports replay. Set
`configuration.webstream.max_connections` and `max_duration_ms` to positive
values to adjust the bounds.

Webstream is deliberately not webhook forwarding: it does not queue, retry, or
fan out messages. It is also not a WebSocket session: clients cannot send
messages after connecting, and Planner does not receive per-message events.
Use a normal `relay` subscription for asynchronous webhooks, a `reply`
destination on a Webstream route for the SSE source, and WebSocket for
bidirectional sessions.

### Go configuration

- `GATEWAY_RUNTIME` - `http` (default) or `diagnostic` for transport-only tests;
  bundled Router commands select their adapters themselves
- `GATEWAY_PLANNER_URL` - Laravel base URL required by `http` mode
- `GATEWAY_PLANNER_SOCKET` - optional Unix socket for a colocated HTTP
  Planner; the Planner URL still supplies its HTTP Host header
- `GATEWAY_INTERNAL_TOKEN` - required shared planner token
- `PORT` - Go host port (default `5001`)
- `MAX_WORKERS` - asynchronous delivery workers (default `100`)
- `MAX_QUEUE_SIZE` - pending relay capacity (default `1000`)
- `REQUEST_TIMEOUT` - outbound request timeout (default `30s`)
- `GATEWAY_AUTOMATION_WORKERS` - concurrent ingress script actors (default `8`)
- `GATEWAY_AUTOMATION_QUEUE_SIZE` - ingress scripts allowed to wait for an
  actor before overload handling (default `32`)
- `GATEWAY_AUTOMATION_TIMEOUT` - total ingress script queue and execution
  deadline (default `10s`)
- `MAX_BODY_SIZE` - ingress and planner response limit (default `10MB`)
- `MAX_IDLE_CONNS`, `MAX_CONNS_PER_HOST`, `IDLE_CONN_TIMEOUT` - HTTP pooling
- `SHUTDOWN_TIMEOUT` - Spinner shutdown timeout (default `30s`)
- DNS listener, timeout, response-bound, and rate-limit settings belong to the
  `webong/dns` resource managed through `/extensions`; zones and Forwarders are
  projected into that desired state by Planner
- SMTP, IMAP, and POP3 listener addresses, TLS files, protocol limits, and
  registered upstreams belong to the revisioned `webong/email` Extension
  configuration. Generic Spinner and Router hosts reject the legacy
  `GATEWAY_SMTP_ADDR`, `GATEWAY_IMAP_ADDR`, and `GATEWAY_POP3_ADDR` settings.
  See [mail forwarding](docs/mail-forwarding.md).
- POP3 endpoint IDs, addresses, and network scope are managed through
  `/email/upstreams`; no exact-host environment list is required
- IMAP endpoint IDs, addresses, and network scope are managed through
  `/email/upstreams`; no exact-host environment list is required
- `GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR` - Planner-owned mailbox credential
  verifier; unset means POP3 login is denied
- Authenticated SMTP and HTTP submission quotas are managed through
  `POST /email/submission-policy`; an authenticator may return per-account
  overrides.
- SMTP recipient preflight, clamd scanning, quarantine storage and retention
  are typed `configuration.smtp` fields on `webong/email`. Custom Planners must
  support the recipient event when preflight is enabled.
- `webong/email` `configuration.failure_notice_domain` enables Email-owned
  delivery-status follow-ups for authenticated, Planner-approved failures.
- `webong/email` `configuration.smtp_relay` owns the relay address, EHLO and
  server names, TLS mode, and timeout. Credentialed relays wait for Extension
  secret references; credentials are rejected from this non-secret object. Email advertises the
  resulting `smtp` adapter dynamically; generic hosts do not parse relay
  environment variables.
- `GATEWAY_DELIVERY_SPOOL_PATH` - durable local spool for asynchronous HTTP
  and SMTP deliveries. Bundled Router launchers
  default it to `GATEWAY_ROUTER_DATA_DIR/deliveries`; standalone Spinner must
  set a persistent path explicitly to make HTTP delivery crash-recoverable.
  It is required when outbound SMTP is enabled. Without it, standalone HTTP
  relays still use the in-process queue.
- `GATEWAY_DELIVERY_RECEIPT_RETENTION` - lifetime of payload-free success
  receipts (default `2160h`, 90 days; `0` disables automatic pruning).
  Pending and failed deliveries are never pruned by this setting.
- `GATEWAY_DELIVERY_ADMIN_ADDR` - optional separate private, token-authenticated
  listener for delivery status and failed-item retry. It requires the spool
  and `GATEWAY_INTERNAL_TOKEN`; see [delivery operations](docs/deliveries.md).
- `GATEWAY_DELIVERY_ADMIN_URL` - Planner's trusted private URL for that listener;
  enables bearer-authenticated public `/deliveries` management routes.
- `GATEWAY_DELIVERY_ADMIN_SHARED_URLS` - ordered JSON list of private admin
  origins using one shared PostgreSQL queue; Planner fails over safe reads and
  idempotent Email submissions. See [delivery operations](docs/deliveries.md).
- `GATEWAY_DELIVERY_MAX_ATTEMPTS` - attempts before moving a delivery to the
  failed spool (default `8`)
- `GATEWAY_DELIVERY_INITIAL_BACKOFF`, `GATEWAY_DELIVERY_MAX_BACKOFF` - retry
  delay range (defaults `5s` and `1h`)
- `GATEWAY_DELIVERY_POLL_INTERVAL` - recovery and due-delivery scan interval
  (default `1s`)

For spooled HTTP deliveries, 2xx completes the attempt. Connection failures,
408, 425, 429, and 5xx retry with bounded backoff; other non-2xx statuses move
to the failed spool after one attempt. The callback response body is not part
of the delivery result. This local spool belongs to one Spinner process; do
not share its path among replicas.

The spool retains payload-free delivered receipts and exposes bounded
`Status`/`List` inspection plus explicit `Retry` for failed items. Planner
proxies its authenticated `/deliveries` management routes to the private
Spinner listener when configured. A retry is at-least-once: a crash after a
destination accepts a request but before Gateway persists the receipt can
still result in a duplicate callback.

PHP-side cache settings:

- `GATEWAY_CACHE_ENABLED` - enables registry route caching (default `true`)
- `GATEWAY_CACHE_STORE` - Laravel cache store; use a shared store such as
  Redis when multiple warm workers or application instances are running
- `GATEWAY_PATH_CACHE_TTL` - path-binding TTL in seconds (default `300`)
- `GATEWAY_ROUTE_CACHE_TTL` - endpoint/subscription snapshot TTL (default
  `30`)
- `GATEWAY_MISSING_CACHE_TTL` - negative lookup TTL (default `5`)
- `GATEWAY_CACHE_PREFIX` - cache-key prefix (default `gateway`)
- `GATEWAY_CACHE_CUSTOM_PROVIDERS` - opt custom `proxy` providers into
  route caching (default `false`)
- `GATEWAY_MUTATION_LOCK_STORE` - shared cache store used for conditional
  subscription mutations; defaults to `GATEWAY_CACHE_STORE`
- `GATEWAY_MUTATION_LOCK_SECONDS` - route lock lease (default `10`)
- `GATEWAY_MUTATION_LOCK_WAIT_SECONDS` - lock wait before `503` (default `5`)

`diagnostic` mode has no Planner wired by itself. Build the combined Router
with `GATEWAY_ROUTER_RUNTIME=frankenphp`, or use `http` to run Spinner against
a separately deployed Planner.

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
channel. The planner then uses `webong/proxy` to resolve the endpoint and
its active destinations. A destination with
`metadata.delivery_mode=reply` becomes the single synchronous reply; all other
HTTP destinations become subscriber relays. PHP-native event and job
destinations are dispatched by `webong/proxy` in the planner process.
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
successful writes. If application code mutates `proxy` directly, invalidate
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

The PHP-facing DSL compiles to a normal `webong/proxy` destination:

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

Planner owns the registry management API. In a distributed deployment set
`REGISTRY_TOKEN`; both management
routes are public through the shared RoadRunner listener and require that
bearer token. Go only transports these ordinary HTTP requests to Laravel.

Create or ensure an endpoint through `webong/proxy`:

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
destination storage are provided by the configured `webong/proxy`
registry; its Laravel migrations remain PHP-owned.

## Managed servers

Gateway core owns the complete managed-server control plane. Integrations only
register a server-technology provider and Go workload; core composes that with
the selected `local`, `docker`, or `kubernetes` runtime driver.

Registered **deployment targets** select an explicit remote Docker context or
Kubernetes cluster/namespace. A target-bound `gateway-provision` worker runs
without Spinner's public traffic listener and uses a target-scoped token.
Integrations are separate executables selected with
`GATEWAY_INTEGRATION_PATHS`; Spinner loads them through the versioned
`github.com/webong/gateway/arranger/integration` protocol. Build the bundled
Reverb, Mercure, and Centrifugo integrations with `scripts/build-gateway-integrations`.
Build `bin/gateway-integration` with `make build-integration-manager` to list,
install, and verify exact versions from the generated local or trusted HTTPS
catalog; activation remains an explicit `GATEWAY_INTEGRATION_PATHS` setting.
See [integrations](docs/integrations.md) for the published
driver SDK, protocol, and third-party authoring example.
See [remote provisioning](docs/provisioning-targets.md) for setup, permission
checks, compatibility, and the remaining lifecycle/routing limitations.

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

Gateway nodes coordinate through the shared control plane. Each node
heartbeats its supported drivers, receives only its deterministic replica-slot
assignments, and reconciles those slots locally. The first live node ID is
reported as the placement leader; an expired node is removed from placement
after `GATEWAY_PROVISIONING_COORDINATION_LEASE_SECONDS` (default `15`). Use
the same lease value on every Gateway node.

The configurable table environment variables remain
`GATEWAY_SERVERS_TABLE`, `GATEWAY_APPLICATIONS_TABLE`, and
`GATEWAY_INSTANCES_TABLE`, `GATEWAY_NODES_TABLE`, and `GATEWAY_TARGETS_TABLE`. Their Laravel keys are under
`gateway.servers.tables`.

See [`pkg/reverb`](pkg/reverb), [`pkg/mercure`](pkg/mercure), and
[`pkg/centrifugo`](pkg/centrifugo) for technology-specific settings and
workload requirements.

## Internal planner routes

The service provider registers `POST /_internal/gateway/plan`. In
`roadrunner` mode, Go/RoadRunner intercepts that path before it can become a
public Laravel route. In `http` mode, the Go edge blocks the path publicly and
calls it only on the configured Planner. The controller requires a
non-empty `GATEWAY_INTERNAL_TOKEN` matching the Go process.

The same provider registers `POST /_internal/gateway/event` for protocol events
such as SMTP transactions and DNS queries. It uses the same internal token and
is intended to be reachable only from the Go listener or the loopback PHP
bridge in separate-process deployments. Embedded Caddy uses the generic
in-process reverse contract instead.

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
`infra/caddy/Caddyfile.frankenphp.example`. Copy it into the Laravel application's
FrankenPHP deployment and set `APP_PUBLIC_PATH`, `SERVER_NAME`, and
`GATEWAY_INTERNAL_TOKEN`.

Build a FrankenPHP binary with this module using the FrankenPHP builder image
or an equivalent `xcaddy` build. From a FrankenPHP source checkout or builder,
add the module path to its existing build command:

```bash
--with github.com/webong/gateway/pkg/caddy \
--replace github.com/webong/gateway=/path/to/gateway \
--replace github.com/webong/gateway/pkg/caddy=/path/to/gateway/pkg/caddy \
--replace github.com/webong/gateway/arranger=/path/to/gateway/mod/arranger \
--replace github.com/webong/gateway/pkg/spinner=/path/to/gateway/pkg/spinner
```

For a custom FrankenPHP Docker image, add the same module to the builder's
`xcaddy build` command, then replace the runtime image's FrankenPHP binary.
The Caddy handler is compiled into the binary; it is not loaded dynamically at
runtime. The handler owns its Go delivery worker pool and drains it during a
Caddy configuration reload.

The custom binary contains only the generic `gateway` host adapter. Activate
`webong/email` through Planner's `/extensions` API to add SMTP, POP3, IMAP, and
the public Email HTTP submission route after deployment. Its child process owns
sibling TCP listeners and calls Planner through Caddy's in-process,
authenticated reverse contract; no PHP-only loopback listener or Caddy rebuild
is required. The generic Caddy host currently provides only the HTTP delivery
adapter: SMTP ingress can forward to HTTP subscribers, but outbound SMTP and
durable HTTP submission fail closed until Email advertises its own durable
adapter capability. Do not enable public HTTP submission in that deployment
shape yet. See [Email forwarding](docs/mail-forwarding.md) for listener
configuration and private-upstream CA trust.

## Tests

Run the Go transport, Arranger contracts, and isolated release-consumer tests
from the repository root:

```bash
make test-go
```

Run Go vet for both modules. The Makefile uses module mode to avoid treating
the PHP Composer `vendor/` directory as Go dependencies:

```bash
make vet
```

Run the PHP test suite with the installed Testbench/Pest tooling:

```bash
vendor/bin/pest
```

The `composer test` script uses Composer's default 300-second process timeout,
which the full Planner suite can exceed. Prefer `vendor/bin/pest` or raise
`COMPOSER_PROCESS_TIMEOUT`.

Run the real Octane worker smoke test using the included minimal Laravel host:

```bash
bash scripts/octane-smoke.sh
```

Run the non-RoadRunner HTTP runtime smoke test. It starts Laravel with PHP's
built-in HTTP server, then starts Go as the public edge:

```bash
bash scripts/http-smoke.sh
```

Run the installable Email Extension smoke. It builds the real DNS and Email
packages, resolves Email's declared DNS dependency, launches both child
processes through the generic Spinner host, sends a real SMTP transaction, and
verifies the durable delivery handoff:

```bash
bash scripts/smtp-smoke.sh
# or: make test-smtp
```

Run the local Email verification suite. It combines the packaged-process smoke,
the complete Go Email package suite, and focused Planner control-plane tests:

```bash
make test-email
```

The production-like extension distribution and container boundary are covered
separately by `scripts/test-kubernetes-email`, which activates DNS and Email
through Planner's `/extensions` API before exposing SMTP.

Run the WebSocket smoke test for both HTTP and RoadRunner. It upgrades a real
connection, sends a message through the PHP protocol planner, and verifies
the subscriber delivery:

```bash
bash scripts/websocket-smoke.sh
# or: make test-websocket
```

Run the authoritative DNS adapter tests, including real UDP and TCP queries:

```bash
make test-dns
```

`make test-dns-integration` builds and installs the `webong/dns` process package
after a PHP-free Spinner host has started, then exercises a Provided answer and
observation over real DNS. It also runs the focused Planner CMS suite for Zones,
Endpoints, Forwarders, revisions, and observations. It does not prove public
delegation, artifact distribution over HTTPS, or redundant nameserver operation.

Verify the optional Caddy integration module:

```bash
make test-frankenphp
```

Set `GATEWAY_OCTANE_APP_PATH` when testing an application-owned Laravel host
instead of the fixture.
