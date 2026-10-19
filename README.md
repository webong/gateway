# Gateway

Gateway Router consists of **Spinner**, the Go transport module, and
**Planner**, the PHP control-plane package. They can be deployed separately or
together in the Gateway Router distribution.

| Distribution | What runs | Use it when |
| --- | --- | --- |
| Gateway Router | Spinner and the bundled Planner behind one public listener, hosted by embedded RoadRunner or FrankenPHP | You want a self-contained Gateway-owned registry |
| Spinner | Go image plus an external PHP Planner | Your Planner lives in an existing Laravel app or a separate service |
| Planner | `webong/gateway` Composer package | Your Laravel app owns route policy and registry context |

The same route-plan and protocol-event contracts connect Spinner and Planner
in both deployment shapes. The optional `ext/frankenphp` Go module embeds the
Planner through the FrankenPHP library without importing the standalone Spinner
command. The legacy Caddy adapter remains optional; the combined Router Docker
image uses the Go-library extension. RoadRunner embeds PHP workers in
`src/spinner/cmd/proxy`. None of these modes
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

Composer distribution archives contain only `composer.json`, `LICENSE`,
`README.md`, `src/planner/` (including configuration and migrations), and the
framework-free PHP interfaces and protocol values in `lib/contract/src/`.
The Go Spinner, extension sources, Go driver library, bundled Router app,
tests, and deployment/build tooling are not downloaded into the consuming
application's vendor directory with these archives. Dependencies are still
resolved normally by Composer.

`.gitattributes` controls GitHub/Git source archives; `archive.exclude` in
`composer.json` controls Composer-built archives. No install-time cleanup or
Composer plugin is needed. Verify both independently with `composer test-package`
from a source checkout.

This applies to releases containing these packaging rules, not existing tags
or cached archives. `--prefer-source` (or a fallback to a Git clone) still
downloads the full repository. Use a Git clone for Router/Spinner builds:
the repository's generated source ZIP/tar downloads are Planner-only too.
The driver release script constructs its own separate release tree and is
unaffected by these root archive rules.

## Repository layout

- `lib/router` assembles Spinner and an injected in-process Planner runtime.
- `ext/frankenphp/cmd/router` is the thin bundled executable in its own Go module.
- `ext/roadrunner/runtime` manages PHP workers without Spinner imports;
  `ext/roadrunner/cmd/router` assembles its bundled deployment through `lib/router`.
- `lib/driver` is the independent `github.com/webong/gateway/driver` Go module
  for workload and runtime-driver contracts. See its [extension and release
  guide](lib/driver/README.md); extension authors can use it without Spinner.
- `lib/contract` is the framework-free PHP seam shared by Planner and its
  optional PHP companion packages; it contains interfaces and protocol values,
  never Planner implementations.
- `src/spinner/cmd/proxy` is the Spinner executable and lifecycle wiring.
- `src/spinner/cmd/bridge` is the transport-neutral Go edge and protocol boundary.
- `src/spinner/cmd/bridge/roadrunner` and `src/spinner/cmd/bridge/caddy` are the optional RoadRunner
  and FrankenPHP/Caddy runtime adapters.
- `src/spinner/internal` contains Go configuration, forwarding, worker, and logging
  implementation details.
- `src/planner` is the PHP Planner package source tree.
- `app/` is the thin PHP host for the bundled Planner, not a conventional
  Laravel application skeleton.
- `src/planner/config/` contains the Laravel Planner package configuration;
  `infra/caddy/` holds Caddy examples used by integration builds.
- `src/planner/Servers`, `src/planner/Reconciliation`, `src/planner/database/migrations`, and
  `src/spinner/provision` are the core managed-server control plane and
  runtime: shared storage, `/servers`, `/applications`, `/instances`, desired
  state, health, scaling, routing, reconciliation, and Local/Docker/Kubernetes
  drivers.
- `ext/reverb` adapts Laravel Reverb configuration and its dynamic application
  provider to the core server-type contracts.
- `ext/mercure` and `ext/centrifugo` adapt their server configuration and
  launch contracts to the same core runtime.

The root Go module keeps native FrankenPHP out of Spinner-only builds;
`ext/frankenphp` and its nested `cmd/router` are separate optional Go modules. The PHP
extension imports no Spinner code; only `lib/router` assembles them. When the
PHP `vendor/` directory exists, use `-mod=mod` for Go commands because that
directory belongs to Composer, not Go.

## Choose a deployment

Run the combined distribution from [docs/router.md](docs/router.md). It
supplies a Planner application, SQLite persistence, migrations, and local
management commands. Choose embedded RoadRunner or FrankenPHP; the Docker
image is optional. No existing Laravel application is required.

Build the Go-only Spinner image from the root `Dockerfile` when Planner is
deployed separately. Set `GATEWAY_LARAVEL_BACKEND_URL` to its private HTTP
address. The Go-only image contains no PHP or Planner application.

## Choose a Spinner host runtime

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
- `diagnostic` starts only Go health and metrics listeners for adapter tests.

For the standalone Spinner executable, set `GATEWAY_RUNTIME` to `http` (the
default), `roadrunner`, or `diagnostic`. The bundled FrankenPHP Router selects
its in-process Planner when launched with `gateway-router library`; it does not
need `GATEWAY_RUNTIME`. The Router launcher sets `roadrunner` when using its
managed PHP workers.

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
go run ./src/spinner/cmd/proxy
```

`GATEWAY_INTERNAL_TOKEN` is required in embedded mode. The token is sent
only on the PHP planner call and must match the Laravel configuration.
The planner path `/_internal/gateway/plan` is reserved and cannot be called
as a public request through the Go middleware.

To run SMTP alongside the embedded RoadRunner HTTP listener, add:

```bash
export GATEWAY_SMTP_ADDR=':2525'
export GATEWAY_SMTP_HOSTNAME='smtp.example.test'
go run ./src/spinner/cmd/proxy
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
go run ./src/spinner/cmd/proxy
```

Go sends `POST /_internal/gateway/plan` to that backend and reverse-proxies
every `pass_through` request to it. The Go public listener still owns the
network edge, while Laravel owns application routing and the control plane.
The backend URL may contain a path prefix; the planner route is appended to
that prefix.

For a colocated Laravel process, retain the same HTTP contract but route it
over a Unix domain socket. The URL supplies the HTTP host and optional path
prefix; Go dials the socket instead of the URL's TCP host:

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_LARAVEL_BACKEND_URL=http://gateway-planner
export GATEWAY_LARAVEL_BACKEND_SOCKET=/run/gateway/planner.sock
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
go run ./src/spinner/cmd/proxy
```

Configure Nginx, Caddy, or another local HTTP server to listen on that socket.
This is an HTTP transport optimization for a single host, not a new PHP worker
or RPC runtime. Do not use a Unix socket when Gateway and Laravel are in
separate containers or nodes; use their private HTTP network address instead.

### SMTP listener

SMTP is available in `http` and `roadrunner` modes. `src/spinner/cmd/proxy` starts the Go
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
go run ./src/spinner/cmd/proxy
```

Each accepted SMTP transaction is sent to
`POST /_internal/gateway/event` as a protocol-neutral event. PHP validates the
message, matches the agnostic endpoint/subscriber registry using the first
recipient as the route key, and returns `accept`, `reject`, or `deliver`. A
`deliver` decision is persisted by Go before the SMTP DATA command is
acknowledged. Set `GATEWAY_DELIVERY_SPOOL_PATH` when SMTP decisions may return
deliveries; without a durable queue Spinner returns a temporary SMTP error
instead of accepting a volatile handoff. In `roadrunner` mode the event call
stays in-process; in `http` mode it
uses the Laravel backend URL. Embedded Caddy/FrankenPHP deployments use the
`gateway_smtp` app below.

The current adapter does not advertise SMTP AUTH unless
`GATEWAY_SMTP_AUTH_ENABLED=true` is explicitly set. AUTH uses the
`authenticate` protocol event, so the Laravel application remains responsible
for validating the username and password. AUTH requires TLS and advertises
PLAIN only after a certificate/key pair is configured. STARTTLS is enabled by
providing the same certificate/key pair; set
`GATEWAY_SMTP_IMPLICIT_TLS=true` for SMTPS-style TLS from the first byte.

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

### Authoritative DNS hooks

DNS hooks are available in `http` and `roadrunner` modes. The Go process opens
sibling UDP and TCP listeners on the same address, parses authoritative DNS
queries, and sends a `protocol=dns`, `kind=query` event to PHP. This is an
authoritative-only service: it refuses queries outside its configured zone and
does not perform recursive resolution.

```bash
export GATEWAY_RUNTIME=http
export GATEWAY_LARAVEL_BACKEND_URL=http://127.0.0.1:8000
export GATEWAY_INTERNAL_TOKEN='use-a-long-random-value'
export GATEWAY_DNS_ADDR=':53'
go run ./src/spinner/cmd/proxy
```

Planner supplies active zones over the authenticated private connection and
Spinner refreshes the complete snapshot without restarting (every 5 seconds by
default). Zone names and nameservers must be created through Planner; the old
DNS zone env snapshots are no longer read. A wildcard
A record is not a substitute for an NS delegation. The listener needs permission
to bind port 53; a container can map public UDP/TCP port 53 to an unprivileged
internal port such as 5353.

The query name format is `[<data-labels>.]<endpoint-token>.<configured-zone>`.
For `aGVsbG8.hook-1.dns.example.com`, Go sends `/hook-1` as the
application-owned route, preserves `aGVsbG8` in the JSON payload, and exposes
these subscription matching fields:

- `protocol=dns`
- `event=query`
- `attributes.qname`, `attributes.qtype`, and `attributes.qclass`
- `attributes.query_id` for the DNS wire transaction ID
- `attributes.endpoint` and `attributes.data`
- `attributes.source_addr` and `attributes.transport`

When the DNS hook control plane is enabled, its resolver automatically maps
`/hook-1` to the PHP-owned endpoint registry entry before delegating non-DNS
requests to the application's configured `PathResolver`. Relay subscriptions
receive the normalized query JSON asynchronously. A
single `reply` subscription is called synchronously; a successful HTTP response
body can contain:

```json
[
  {"type": "a", "value": "192.0.2.10"},
  {"type": "aaaa", "value": "2001:db8::10"},
  {"type": "mx", "value": "mail.example.test", "preference": 10},
  {"type": "txt", "value": "hello world"}
]
```

Supported response types are `a`, `aaaa`, `mx`, `cname`, and `txt`. MX requires
an integer `preference` from 0 to 65535 and a valid exchange hostname. A
single null MX uses `{"type":"mx","value":".","preference":0}` to indicate
that a name accepts no email; it cannot be combined with another MX. Only
records relevant to the requested type are returned. A CNAME may be returned
alone, but cannot coexist with other record types for the same name. Newlines
in TXT values become separate strings. Invalid response JSON,
record values, non-2xx reply responses, and planner timeouts produce `SERVFAIL`.
Missing routes produce authoritative `NXDOMAIN`; queries without an answer
produce authoritative `NOERROR`/NODATA.

The default planner deadline is 750ms. Related settings are
`GATEWAY_DNS_PLANNER_TIMEOUT`, `GATEWAY_DNS_TTL` (default `0`),
`GATEWAY_DNS_SOA_EMAIL`, `GATEWAY_DNS_READ_TIMEOUT`,
`GATEWAY_DNS_WRITE_TIMEOUT`, and `GATEWAY_DNS_MAX_RESPONSE_RECORDS` (default
`16`). `GATEWAY_DNS_RATE_LIMIT` and `GATEWAY_DNS_RATE_BURST` cap total DNS
queries per second (defaults `5000` and `10000`). `ANY` queries are refused and
UDP responses are capped at 1232 bytes.
`GATEWAY_DNS_ZONE_REFRESH_INTERVAL` controls how quickly a Spinner observes API
zone changes (default `5s`). A failed or invalid refresh retains the last valid
snapshot.
Use unique data labels when each lookup must create a distinct event. Lowercase
base32 data is safer than case-sensitive standard base64 because
DNS names are case-insensitive and each label is limited to 63 bytes.

### DNS hook product API

The DNS control plane is available through Planner. Domains and their
authoritative nameservers are created through the API and stored as ordinary
WebProxy endpoints; they are not duplicated in Spinner env.

Create any number of authoritative zones with the bearer-token-protected API:

```bash
curl -X POST https://relay.example.test/dns/zones \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"hooks.example.com","nameservers":["ns1.example.net","ns2.example.net"]}'
```

`provider=manual` records the desired delegation but does not modify external
DNS. The built-in `cloudflare` provider automates an NS delegation for a child
zone whose parent is managed by the configured Cloudflare account:

```bash
export GATEWAY_DNS_CLOUDFLARE_TOKEN='scoped-api-token'
curl -X POST https://relay.example.test/dns/zones \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"hooks.example.com","provider":"cloudflare","nameservers":["ns1.example.net","ns2.example.net"]}'
```

The token is a deployment secret and needs permission to read zones and edit DNS
records. Gateway auto-discovers the managed parent; `provider_options` may
instead include `parent_zone_id` and `parent_zone`. Apex registrar nameserver
changes remain registrar-specific and require a registrar adapter or manual
delegation. Applications can register additional `DnsProvider` implementations
in `DnsProviderRegistry`.

The listener chooses the longest matching zone for nested delegations. A hook
creation request must include `zone` when more than one zone is active; with one
zone it remains optional. The package registers a built-in
`gateway` WebProxy client, provisions ordinary managed WebProxy endpoints for
the DNS zone and each hook, and wraps the application's existing path resolver
with token lookup. Gateway-specific attributes live in the endpoint's reserved
`metadata._gateway` object, including the kind, protocol, hostname, route, and
parent zone endpoint key. No DNS-specific endpoint or destination storage is
added to `webong/proxy`.

DNS history uses the generic `events` table, linked to the
underlying WebProxy endpoint. Opt-in Webhook, WebSocket, and Webstream planning
history uses that same table, with bounded body capture and a shared retention
policy; see [endpoint event history](docs/events.md). No protocol-specific
history tables are needed.

Create a hook through the bearer-token-protected management API:

```bash
curl -X POST https://relay.example.test/dns/hooks \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "external_id": "workspace-42-login-check",
    "zone": "dns.example.com",
    "name": "Login verification",
    "metadata": {"workspace": "workspace-42"}
  }'
```

The response includes an opaque `token`, its `hostname`, the
`{data}.<token>.<zone>` query template, the underlying `endpoint_key`, and URLs
for subscriptions, event history, and usage. Repeating the same `external_id`
within a zone is idempotent and returns the same token. Hook IDs and tokens are
unambiguous across zones; `GET /dns/zones` lists the configured zone names and
nameservers, and `GET /dns/hooks?zone=...` filters hooks by zone.

Attach subscribers through the existing WebProxy registry API using the
returned endpoint key and the canonical DNS route:

```json
{
  "subscriber_id": "workspace-42",
  "subscription_id": "workspace-42-dns",
  "type": "relay",
  "webhook_group": "dns",
  "routing_scope": "dns",
  "routing_key": "query",
  "url": "https://subscriber.example.com/dns",
  "match": {
    "version": "v1",
    "rules": {"attributes.qtype": ["required", "in:TXT"]}
  }
}
```

The UI-ready management surface is:

```text
GET    /dns/zones
POST   /dns/zones
GET    /dns/zones/{id-or-name}
POST   /dns/zones/{id-or-name}/provision
DELETE /dns/zones/{id-or-name}
POST   /dns/hooks
GET    /dns/hooks
GET    /dns/hooks/{id-or-token}
PATCH  /dns/hooks/{id-or-token}
DELETE /dns/hooks/{id-or-token}
GET    /dns/hooks/{id-or-token}/events?type=TXT&transport=udp&limit=50
GET    /dns/hooks/{id-or-token}/usage?from=2026-08-01&to=2026-08-31
```

History records the normalized query and PHP planning decision. Recording is
idempotent by Gateway event ID, and write exceptions do not replace a completed
route decision. Usage exposes total queries, daily counts, and counts by query
type and transport. Pausing or deleting a hook invalidates its cached binding
immediately; the underlying WebProxy endpoint is retained so subscription and
audit records are not orphaned.

`GATEWAY_DNS_PROXY_CLIENT` customizes the built-in client. Hook identity uses
WebProxy endpoints, and history uses the generic `events` table; the historical
`GATEWAY_DNS_HOOKS_TABLE` and `GATEWAY_DNS_EVENTS_TABLE` settings are not used.
Disable or remove individual DNS resources through Planner. The DNS listener
itself remains a deployment decision.

### Spinner container

Build the Gateway router image with:

```bash
docker build -t webong/gateway-spinner:local .
```

The image runs as an unprivileged `gateway` user and exposes the Go router's
HTTP (`5001`), optional SMTP (`2525`), and unprivileged DNS (`5353` TCP/UDP)
listeners. It intentionally does not contain a Laravel application: deploy the
Gateway Laravel planner as its companion application and point the router at it
with `GATEWAY_LARAVEL_BACKEND_URL`, or mount a colocated planner socket using
`GATEWAY_LARAVEL_BACKEND_SOCKET`.

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

The root Docker image exposes HTTP and unprivileged TCP/UDP port 5353. The
included `docker-compose.dns.example.yml` publishes both DNS transports on
public port 53:

```bash
GATEWAY_LARAVEL_BACKEND_URL=https://app.internal.example \
GATEWAY_INTERNAL_TOKEN=replace-me \
docker compose -f docker-compose.dns.example.yml up -d
```

Configure `REGISTRY_TOKEN`, run the
Gateway and WebProxy migrations on Planner. Create each domain through
`POST /dns/zones`; every Spinner observes active zones over its authenticated
Planner connection without restarting. At each parent DNS provider, delegate
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
default, and individual messages remain limited by `MAX_BODY_SIZE`. Configure
`GATEWAY_WEBSOCKET_MAX_CONNECTIONS` and `GATEWAY_WEBSOCKET_MAX_DURATION` for
standalone Spinner and bundled Router deployments, or
`websocket_max_connections` and `websocket_max_duration` in the Caddy `gateway`
block. Limits apply per process, not across a cluster.

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

Production Spinner blocks private SSE source addresses by default, even when
the source was selected by Planner. First deploy a narrow network ceiling on
Spinner, then create an exact-origin source grant through the authenticated
Planner API:

```sh
GATEWAY_WEBSTREAM_PRIVATE_NETWORK_CIDRS='10.42.7.0/24'
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

Private-source grants are created only through Planner; there is no env origin
map. `GATEWAY_WEBSTREAM_PRIVATE_NETWORK_CIDRS` remains a deployment safety
ceiling and cannot itself grant a private source.
Unset it after migrating its grants to the API; while set, it can still grant
an origin independently of the Planner resource.

For GET, Spinner emits `retry: 3000` by default so conforming SSE clients reconnect. On
reconnect, `Last-Event-ID` is supplied to Planner as `last_event_id` and the
built-in registry planner forwards it only to the selected source. It emits a
comment heartbeat every 15 seconds by default when the source is between SSE
events, never inside an incomplete event; configure
`GATEWAY_WEBSTREAM_RETRY` and `GATEWAY_WEBSTREAM_HEARTBEAT_INTERVAL` (set the
heartbeat interval to `0` to disable it). `/metrics` exposes
`webstreams_active`, `webstreams_opened_total`, and `webstreams_failed_total`
in the standalone Spinner runtime.

Each Spinner/Router process accepts at most 1000 concurrent Webstreams by
default, counting streams being planned or opened. Further attempts receive
HTTP 503 with `Retry-After: 1` before Planner or the upstream is called.
Streams close after 24 hours by default so clients reconnect with their
`Last-Event-ID` if the source supports replay. Set
`GATEWAY_WEBSTREAM_MAX_CONNECTIONS` and `GATEWAY_WEBSTREAM_MAX_DURATION` to
positive values to adjust the bounds. The Caddy module uses
`webstream_max_connections` and `webstream_max_duration` in its `gateway`
block and shares the admission limit across requests.

Webstream is deliberately not webhook forwarding: it does not queue, retry, or
fan out messages. It is also not a WebSocket session: clients cannot send
messages after connecting, and Planner does not receive per-message events.
Use a normal `relay` subscription for asynchronous webhooks, a `reply`
destination on a Webstream route for the SSE source, and WebSocket for
bidirectional sessions.

### Go configuration

- `GATEWAY_RUNTIME` - `http` (default), `roadrunner`, or `diagnostic` for
  transport-only tests; the combined image uses FrankenPHP instead
- `GATEWAY_LARAVEL_BACKEND_URL` - Laravel base URL required by `http` mode
- `GATEWAY_LARAVEL_BACKEND_SOCKET` - optional Unix socket for a colocated HTTP
  Laravel backend; the backend URL still supplies its HTTP Host header
- `GATEWAY_INTERNAL_TOKEN` - required shared planner token
- `PORT` - Go host port (default `5001`)
- `ROADRUNNER_CONFIG` - RoadRunner YAML path (default `.rr.yaml`)
- `MAX_WORKERS` - asynchronous delivery workers (default `100`)
- `MAX_QUEUE_SIZE` - pending relay capacity (default `1000`)
- `REQUEST_TIMEOUT` - outbound request timeout (default `30s`)
- `MAX_BODY_SIZE` - ingress and planner response limit (default `10MB`)
- `MAX_IDLE_CONNS`, `MAX_CONNS_PER_HOST`, `IDLE_CONN_TIMEOUT` - HTTP pooling
- `SHUTDOWN_TIMEOUT` - Spinner shutdown timeout (default `30s`)
- `GATEWAY_DNS_ADDR` - enables authoritative DNS over UDP and TCP
- `GATEWAY_DNS_ZONE_REFRESH_INTERVAL` - Planner zone snapshot refresh interval
  for HTTP and bundled RoadRunner runtimes (default `5s`)
- `GATEWAY_SMTP_ADDR` - enables the sibling Go SMTP listener in `http` or
  `roadrunner` mode
- `GATEWAY_POP3_ADDR` - enables the implicit-TLS POP3 upstream proxy in standalone
  Spinner or the bundled Router; see [mail forwarding](docs/mail-forwarding.md)
- `GATEWAY_POP3_TLS_CERT_FILE`, `GATEWAY_POP3_TLS_KEY_FILE` - required PEM
  certificate and key for POP3 client connections
- POP3 endpoint IDs, addresses, and network scope are managed through
  `/email/upstreams`; no exact-host environment list is required
- `GATEWAY_POP3_AUTH_PER_MINUTE`, `GATEWAY_POP3_AUTH_BURST` - per-socket-peer
  password-attempt limit local to each Spinner process (defaults `30` and `5`)
- `GATEWAY_IMAP_ADDR` - enables the implicit-TLS IMAP upstream proxy; see
  [mail forwarding](docs/mail-forwarding.md)
- `GATEWAY_IMAP_TLS_CERT_FILE`, `GATEWAY_IMAP_TLS_KEY_FILE` - required IMAP
  listener certificate and key
- IMAP endpoint IDs, addresses, and network scope are managed through
  `/email/upstreams`; no exact-host environment list is required
- `GATEWAY_IMAP_AUTH_PER_MINUTE`, `GATEWAY_IMAP_AUTH_BURST` - per-socket-peer
  password-attempt limit local to each Spinner process (defaults `30` and `5`)
- `GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR` - Planner-owned mailbox credential
  verifier; unset means POP3 login is denied
- `GATEWAY_SMTP_HOSTNAME` - SMTP greeting/domain (default `gateway.local`)
- `GATEWAY_SMTP_MAX_MESSAGE_SIZE` - accepted message limit (default `10MB`)
- `GATEWAY_SMTP_MAX_RECIPIENTS` - recipients per transaction (default `100`)
- `GATEWAY_SMTP_MAX_CONNECTIONS` - simultaneous SMTP connections per Spinner
  process (default `1000`); additional connections wait in the TCP backlog
- `GATEWAY_SMTP_MESSAGES_PER_MINUTE`, `GATEWAY_SMTP_MESSAGE_BURST` - per-source
  IP rate limit on SMTP `MAIL` commands (defaults `120` per minute with a
  burst of `20`). The limit is local to each Spinner process.
- `GATEWAY_SMTP_READ_TIMEOUT`, `GATEWAY_SMTP_WRITE_TIMEOUT` - SMTP socket
  timeouts
- `GATEWAY_SMTP_PLANNER_TIMEOUT` - PHP planning timeout (default `30s`)
- `GATEWAY_SMTP_TLS_CERT_FILE`, `GATEWAY_SMTP_TLS_KEY_FILE` - PEM certificate
  and private-key files for STARTTLS or implicit TLS
- `GATEWAY_SMTP_IMPLICIT_TLS` - use implicit TLS instead of plain SMTP with
  STARTTLS (default `false`)
- `GATEWAY_SMTP_AUTH_ENABLED` - require PHP-planned SMTP AUTH (default
  `false`; TLS is required). The AUTH decision must grant exact envelope
  senders, and the message's visible From address must also be granted.
- `GATEWAY_EMAIL_DKIM_RESOLVER_URL` - private Planner origin for per-domain
  DKIM keys managed through `/email/dkim-keys`; see
  [mail forwarding](docs/mail-forwarding.md)
- Authenticated SMTP and HTTP submission quotas are managed through
  `POST /email/submission-policy`; an authenticator may return per-account
  overrides.
- `GATEWAY_SMTP_RECIPIENT_PREFLIGHT` - `smtp`/`recipient` planning at
  each `RCPT TO`; defaults on when an outbound relay is configured and off
  otherwise. Custom planners must support this event or explicitly set `false`.
- `GATEWAY_EMAIL_CLAMD_NETWORK`, `GATEWAY_EMAIL_CLAMD_ADDR`,
  `GATEWAY_EMAIL_CLAMD_TIMEOUT` - optional private `clamd` scanner connection
  (`tcp` or `unix`, default timeout `10s`); scanner failures defer SMTP DATA
- `GATEWAY_EMAIL_QUARANTINE_PATH` - optional private, persistent directory for
  malware-positive mail; requires clamd. See [mail forwarding](docs/mail-forwarding.md).
- `GATEWAY_EMAIL_QUARANTINE_RETENTION` - optional Go duration for automatic
  quarantine deletion (default `0`, disabled); requires a quarantine path
- `GATEWAY_EMAIL_DSN_DOMAIN` - optional reporting hostname for durable SMTP
  failure notices from authenticated, Planner-approved asynchronous deliveries;
  requires an outbound relay and spool (see [mail forwarding](docs/mail-forwarding.md))
- `GATEWAY_SMTP_RELAY_ADDR` - enables SMTP delivery through a configured relay
  (for example `smtp.example.com:587`)
- `GATEWAY_SMTP_RELAY_LOCAL_NAME` - EHLO name sent by Gateway (default
  `gateway.local`)
- `GATEWAY_SMTP_RELAY_SERVER_NAME` - optional TLS server-name override; by
  default it is inferred from the relay address
- `GATEWAY_SMTP_RELAY_USERNAME`, `GATEWAY_SMTP_RELAY_PASSWORD` - optional
  relay credentials; both are required together and authentication requires
  TLS
- `GATEWAY_SMTP_RELAY_TLS_MODE` - `starttls` (default), `implicit`, or `none`;
  use `none` only for a trusted local/private test relay
- `GATEWAY_SMTP_RELAY_TIMEOUT` - outbound SMTP connection and submission
  deadline (default `30s`)
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

`diagnostic` mode has no Planner wired by itself. Deploy the combined
FrankenPHP image for Gateway Router, or use `http` to run Spinner against
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

Gateway core owns the complete managed-server control plane. Extensions only
register a server-technology provider and Go workload; core composes that with
the selected `local`, `docker`, or `kubernetes` runtime driver.

Registered **deployment targets** select an explicit remote Docker context or
Kubernetes cluster/namespace. A target-bound `gateway-provision` worker runs
without Spinner's public traffic listener and uses a target-scoped token.
Workload extensions are separate executables selected with
`GATEWAY_EXTENSION_PATHS`; Spinner loads them through the versioned
`github.com/webong/gateway/driver/workloadplugin` protocol. Build the bundled
Reverb, Mercure and Centrifugo executables with `scripts/build-gateway-extensions`.
Build `bin/gateway-extension` with `make build-extension-manager` to list,
install, and verify exact versions from the generated local or trusted HTTPS
catalog; activation remains an explicit `GATEWAY_EXTENSION_PATHS` setting.
See [executable extensions](docs/executable-extensions.md) for the published
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

See [`ext/reverb`](ext/reverb), [`ext/mercure`](ext/mercure), and
[`ext/centrifugo`](ext/centrifugo) for technology-specific settings and
workload requirements.

## Internal planner routes

The service provider registers `POST /_internal/gateway/plan`. In
`roadrunner` mode, Go/RoadRunner intercepts that path before it can become a
public Laravel route. In `http` mode, the Go edge blocks the path publicly and
calls it only on the configured Laravel backend. The controller requires a
non-empty `GATEWAY_INTERNAL_TOKEN` matching the Go process.

The same provider registers `POST /_internal/gateway/event` for protocol events
such as SMTP transactions and DNS queries. It uses the same internal token and
is intended to be reachable only from the Go listener or the loopback PHP
bridge in the Caddy deployment.

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
--with github.com/webong/gateway/src/spinner/cmd/bridge/caddy \
--replace github.com/webong/gateway=/path/to/gateway \
--replace github.com/webong/gateway/driver=/path/to/gateway/lib/driver \
--replace github.com/webong/gateway/ext/roadrunner=/path/to/gateway/ext/roadrunner
```

For a custom FrankenPHP Docker image, add the same module to the builder's
`xcaddy build` command, then replace the runtime image's FrankenPHP binary.
The Caddy handler is compiled into the binary; it is not loaded dynamically at
runtime. The handler owns its Go delivery worker pool and drains it during a
Caddy configuration reload.

The same custom binary also contains the `gateway.smtp`, `gateway.pop3`, and
`gateway.imap` Caddy app modules. They start sibling TCP listeners from the
FrankenPHP/Caddy process; these protocols do not pass through `php_server`.
Each app calls the configured private `planner_url` for decisions. The SMTP
app owns its Go delivery pool, while POP3 and IMAP tunnel authenticated
sessions to operator-registered TLS mailbox upstreams. The example file binds
that PHP bridge
to `127.0.0.1:8081` and configures it with `gateway_smtp` in the global block.
This provides the second deployment shape without duplicating protocol
implementations. The SMTP app accepts `tls_cert_file`, `tls_key_file`,
`implicit_tls`, and `auth_enabled` options with the same semantics as the
`GATEWAY_SMTP_*` settings. See [Email forwarding](docs/mail-forwarding.md)
for the POP3/IMAP Caddy options and private-upstream CA trust.

## Tests

Run the Go transport, driver contracts, and isolated release-consumer tests
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

Run the managed Email smoke test across the public domain/alias API, private
Planner, Go SMTP listener, and a loopback outbound relay. It verifies a
two-alias transaction becomes one three-recipient relay handoff, and checks
temporary relay refusal and permanent recipient rejections:

```bash
make test-email
```

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

`make test-dns-integration` starts a local Planner and Spinner, creates hooks
in two zones through the management API, queries one over UDP and the other
over TCP, and checks cross-zone rejection and recorded query history. It uses
`dig` and a temporary SQLite database; it does not prove public delegation or
redundant nameserver operation.

Verify the Caddy module and, when Docker is available, build a FrankenPHP
image containing Gateway's HTTP, SMTP, POP3, and IMAP modules:

```bash
make test-frankenphp
GATEWAY_FRANKENPHP_BUILD=1 make test-frankenphp
```

Set `GATEWAY_OCTANE_APP_PATH` when testing an application-owned Laravel host
instead of the fixture.
