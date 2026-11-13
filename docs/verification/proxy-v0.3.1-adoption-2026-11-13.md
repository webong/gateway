# Proxy v0.3.1 adoption (2026-11-13)

Gateway now consumes `webong/proxy` v0.3.1 in PHP and Go. This delivers
[webong/gateway#19](https://github.com/webong/gateway/issues/19), retargeted from
v0.3.0 to v0.3.1: v0.3.1 keeps the v0.3.0 transport APIs and JSON wire
`version: 1`, and fixes SQL registry portability. Nothing here changes
`app/config.php`'s connection list or `scripts/planner-start`'s PostgreSQL
requirement; Gateway does not support MySQL at runtime.

## Versions

| | Resolved to |
| --- | --- |
| Composer | `webong/proxy` `^0.3.1` in the root manifest, `app/`, every `pkg/*` package that required it, and `pkg/planner`'s `suggest` text. Both `composer.lock` and `app/composer.lock` hold `v0.3.1` at `8362bbe98fcff9c549c062cd0088d38da9a32bbd`. No other dependency moved. |
| Go | `github.com/webong/proxy v0.3.1` (one module; `httpprofile`, `httpclient`, `socketprofile`, `tcpdial` and `udpdial` are its packages). Same commit `8362bbe9…`; `go.sum` carries `h1:L7HbB0aPspnlCI3ZAMlw5ETYnk2kxiEr4FrdP1Ncu/w=` from sum.golang.org. No `replace` directive points at Proxy. |
| Go modules touched | `pkg/spinner` (direct) and the modules that build it: `pkg/spinner/cmd/gateway`, `tools/spinner`, `pkg/roadrunner/cmd/router`, `pkg/frankenphp/cmd/router`, `pkg/caddy`, `pkg/mercure`, `pkg/centrifugo` (indirect). |
| Split tool | `scripts/split-packages` publishes each package's committed `composer.json` and rewrites only Gateway-internal `dev-main` requirements, so the new constraint reaches releases unchanged. `scripts/test-split-packages` passes. |

`udpdial` and `tcpdial` come with the module; Gateway imports neither (issue #19
excludes raw TCP/UDP). `tcpdial` is used inside `httpclient` for SOCKS.

## PHP

`EndpointHttpTransportResolver` no longer translates or validates proxy URLs,
bypass rules or timeouts. It hands the profile configuration to Proxy's
`HttpProfileNormalizer` and builds Gateway's `HttpTransport` wire value from the
resulting portable `HttpProfile`. It keeps what Proxy leaves outside the profile:
profile lookup with its messages (`is not active`, `HTTP-compatible driver`),
`options.headers` for Webstream sources with the reserved-header list
(`Connection`, `Content-Length`, `Host`, `Last-Event-ID`, `Transfer-Encoding`,
`Upgrade`) and line-break checks, the `proxy must be non-empty` rule, and the
`HttpTransport` wire class.

`GuzzleOptions` is not used: Gateway builds no profile-based Guzzle client. The
PHP side only exports the profile; Spinner makes the outbound request. The other
Laravel `Http::` calls (schedule notifications, delivery admin, email status) are
fixed internal calls, not endpoint profiles.

### Deliberate validation differences

| Configuration | Before | Now |
| --- | --- | --- |
| Proxy only under `options.proxy` | Ignored (direct delivery) | Used, like Proxy's native Guzzle client; a top-level `proxy` wins |
| Bypass entry CIDR, port, IPv6, `*.example.test` | Accepted by PHP and honoured by Go (`x/net/http/httpproxy`) | Rejected in PHP (422) and by Spinner. Allowed: host names, IPv4 addresses, leading-dot domains, `*` |
| Bypass list as a map | Accepted (values used) | Rejected: must be a list |
| Proxy URL with a path, query or fragment; control bytes in credentials; SOCKS username without password | Accepted if scheme and host parsed | Rejected |
| `proxy: {}` / `proxy: ""` | Rejected | Still rejected (kept as a Gateway rule, because Proxy's normalizer would turn `{}` into "direct") |
| Timeouts | Numeric, above 0, at most 300 | Same |
| Error text | `Proxy profile [x] …` | `Proxy profile [x] is not a valid portable HTTP profile: …` for Proxy's rules; the other messages are unchanged. Proxy's messages never repeat credentials |

## Go

`Forwarder.clientFor` builds each per-delivery client with
`httpclient.New(httpprofile.Profile, base)`, where `base` is the Forwarder's own
`http.Transport`, whose dialer is Gateway's address policy. Proxy selection,
bypass matching, SOCKS routing and the connect and request timeouts are Proxy's.
Gateway keeps the bounded client cache (128, oldest evicted and its idle
connections closed), the shared default client for deliveries with no profile
settings, the deployment request timeout when a profile omits one, private
callbacks (no proxy, canonical CIDRs inside the deployment ceiling, restricted
dialer, no redirects) and the response-size limit. The two copies of the
wire-to-forwarder translation (`bridge/relay_executor.go`, `bridge/httpout`) now
share `forwarding.TransportFromWire`. A profile Proxy rejects returns
`forwarding.ErrInvalidTransport`, which the queued HTTP adapter treats as a
permanent failure instead of retrying.

### Behaviour that differs from the `httpproxy` implementation

- **Loopback.** `httpproxy` never proxied `localhost` or loopback targets. Proxy's
  profile has no such rule, so Gateway keeps it: a loopback target is dialed
  directly and refused by the address policy (`loopbackDirect`).
- **Connect timeout** now limits the whole dial (DNS, every candidate address)
  and the TLS handshake. It limited each connection attempt before.
- **`socks5://` resolves the target host in Spinner**, `socks5h://` leaves that to
  the proxy. Go's standard library treated both as remote resolution. An
  `http`/`https` proxy is unchanged.
- **Invalid bypass entries** (CIDR, port, IPv6, `*.` wildcard) and timeouts above
  300 s are refused at forward time; the Arranger wire check still only
  validates what it did before.
- **Not adopted:** `pkg/web/spinner/source.go` (Webstream) and the WebSocket
  dialer still build their own proxy handling from the same wire fields. The
  Planner now only emits the portable subset, so the two agree for
  Planner-originated profiles. Moving them is a separate change: `httpclient`
  sets a whole-request client timeout that a stream cannot use.

## Evidence

Counts are from this worktree on macOS arm64, PHP 8.5.8 (libcurl 8.21.0), Go 1.27.2.

| Check | Command | Result |
| --- | --- | --- |
| Full PHP suite | `vendor/bin/pest --compact` with PostgreSQL 18.2 and MySQL 8.4.13 | 487 passed, 2 skipped (4,561 assertions), 0 failed. The two skips are the PostgreSQL verified-TLS tests, which need `GATEWAY_TEST_POSTGRES_SSLROOTCERT` |
| New PHP tests | `vendor/bin/pest tests/Feature/EndpointHttpTransportResolverTest.php tests/Feature/ProxyRegistryUpgradeTest.php` | 66 passed (265 assertions): 58 resolver tests and 8 registry tests across SQLite, PostgreSQL and MySQL |
| Forwarder | `GOWORK=off go -C pkg/spinner test -race -count=3 ./internal/forwarding` | ok (23 top-level tests, 47 subtests, 1 skipped when run alone) |
| Spinner module | `GOWORK=off go -C pkg/spinner test ./...` | 24 packages `ok`, 0 failed |
| Dependent modules | `GOWORK=off go -C <module> test ./...` for `mod/arranger`, `tools/spinner`, `pkg/{caddy,centrifugo,mercure,roadrunner,roadrunner/cmd/router,spinner/cmd/gateway,web,dns,email,reverb,shell,javascript,lua}` | all `ok` |
| Module boundaries | `scripts/test-{runtime-module,router,integration,traffic-extension,automation-extension}-boundaries`, `test-arranger-release`, `test-contract-package` | all exit 0 |
| Split tool | `scripts/test-split-packages` | passes |

`pkg/frankenphp/cmd/router` was not built here: FrankenPHP needs ZTS PHP headers
(`php.h`) this machine lacks. Its module graph resolves (`go list -deps -test`),
and the module needs only the added `go.mod` line.

What the new tests cover, against the acceptance list in the issue:

- **Parity fixture.** `mod/arranger/integration/testdata/http_transport_profiles.json`
  holds 12 profiles, 26 invalid profiles and 8 invalid wire transports. The PHP
  test registers each profile, resolves it and compares the wire it produces, and
  checks it equals Proxy's own export (`GuzzleClientFactory::profile()`). The Go
  test decodes the same wire and checks which address each request is first
  dialed to (proxy or target), covering HTTP and HTTPS proxy selection, bypass
  matching (suffix, case, trailing dot, IPv4, `*`), omitted, default and explicit
  timeouts, and authentication.
- **PHP planner to Go forwarding.** A Pest test plans a real subscription,
  serializes the delivery, and runs a Go test that forwards that JSON through a
  CONNECT proxy to a TLS server, checking proxy credentials, headers and body.
- **Loopback integration.** HTTP, HTTPS, SOCKS5 and SOCKS5h with and without
  credentials; TLS trust failure and success through direct, CONNECT and SOCKS
  paths with Gateway's own `TLSClientConfig`; proxy credentials reach only the
  proxy hop.
- **Policy.** With the production address policy, proxy hops at loopback,
  private, link-local, shared-space, IPv6 and IPv4-mapped addresses are refused
  and never contacted (HTTP and SOCKS); loopback targets are not sent to a proxy;
  a redirect to a blocked IPv6 address is refused; private callbacks reject every
  proxy field and do not follow redirects; environment proxy variables are never
  used.
- **Lifecycle.** Connect deadline applies only to the dial; request timeout and
  cancellation; response-size limit; client reuse; bounded eviction releasing idle
  connections; `CloseIdleConnections` at shutdown.

## Registry upgrade

Proxy 0.3.1's `2026_11_12_000001_use_portable_subscription_identity` migration is
run by the same `artisan migrate --realpath --path=…/webong/proxy/database/migrations`
that `scripts/planner-start migrate` (and `serve`) and `scripts/router-start` use,
so the launchers apply it with no change. Operator steps are in
[Deployment](../deployment.md#upgrading-to-proxy-031-subscription-identity-migration).

**Through the real launcher** (disposable `postgres:18.2`, the
`gateway-planner-bench:local` image with this worktree mounted at `/gateway`,
running `/gateway/scripts/planner-start migrate`):

- Fresh database: Gateway's migration, then the five Proxy migrations including
  the portable identity one, then the package migrations. Succeeds.
- Registry built with the Proxy 0.2.0 migrations (verbatim, in
  `tests/Fixtures/proxy-0.2.0-migrations/`) holding 5 subscriptions (case and
  trailing-space variants, Unicode, and 255-character values in all seven identity
  columns): `planner-start migrate` applied only
  `2026_11_12_000001_use_portable_subscription_identity` from the Proxy set. Row
  count 5, 5 distinct 64-character hashes, indexes `subscriptions_pkey`,
  `subscriptions_registration_hash_unique`, `subscriptions_routing_index`
  (`subscriptions_registration_unique` dropped), `registration_hash` is
  `character(64) NOT NULL`. An MD5 over every pre-existing column of every row,
  ordered by id, was identical before and after
  (`4fcd28ab4b73932c3cfb9c4238b6819f`). A second run reported `Nothing to migrate`.

**As a test** (`tests/Feature/ProxyRegistryUpgradeTest.php`, SQLite always,
PostgreSQL with `GATEWAY_TEST_POSTGRES_*`, MySQL with `GATEWAY_TEST_MYSQL_*`):
fresh install, upgrade from 0.2.0 (rows unchanged, hashes equal Proxy's
`SubscriptionIdentity::hash`, uniqueness enforced, a writer that omits the hash is
refused after the upgrade, second run is a no-op), and the MySQL cases below.

## MySQL 8.4 evidence (not runtime support)

Disposable `mysql:8.4` (8.4.13, server default character set `utf8mb4`). The test
creates the database as `utf8mb4`/`utf8mb4_unicode_ci` and connects with Laravel's
stock MySQL settings, which is also what Proxy's own MySQL suite uses:

- Proxy 0.2.0's subscriptions migration fails with `Specified key was too long`
  (error 1071), asserted by a test.
- All five Proxy 0.3.1 migrations apply to an empty database; the table is
  utf8mb4; subscriptions with 255-character values in all seven identity columns
  insert; the same registration cannot be inserted twice; a second run does
  nothing.
- Rolling the portable identity back raises Proxy's refusal and leaves the schema
  and rows intact.

This shows Proxy's registry no longer blocks MySQL. It does not show that
Gateway's own Planner schema, queries and hosts are MySQL-ready beyond what
`GatewayMigrationCommandTest` already covers for `gateway:migrate`.

## Runtime requirements and remaining limits

- PHP 8.3 or newer (Gateway's `^8.3`; Proxy tests 8.3, 8.4 and 8.5, with Laravel
  12 on all and Laravel 13 on 8.4 and 8.5). Go 1.26 (`pkg/spinner/go.mod`);
  Proxy's own module minimum is 1.23.
- libcurl 8.23 or newer is needed only by a PHP caller that builds an
  authenticated-SOCKS Guzzle client through `GuzzleOptions`. Gateway builds none;
  Spinner (Go) makes those requests. Authenticated SOCKS through Spinner was
  tested directly (above).
- A remote proxy resolves its own destination, so Gateway's address checks cover
  the hop to the proxy and not what the proxy then dials. Giving someone permission
  to set a profile proxy delegates outbound policy to that proxy; see the
  [egress audit](outbound-egress-audit-2026-11-04.md).
- The PHP-to-Go end-to-end test runs when Go at least the version in
  `pkg/spinner/go.mod` is on `PATH`, and is skipped otherwise.
- Not run: `pkg/frankenphp/cmd/router` build and tests (ZTS PHP headers), the
  Docker and Kubernetes smoke scripts, and any load or latency comparison.
