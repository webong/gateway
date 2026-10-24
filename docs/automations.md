# Gateway Automations

Gateway Automations run small, bounded ingress logic after Laravel has matched
and authorized a route. The standalone Gateway Driver module owns the
language-neutral execution interface, dispatcher, and output validation.
Spinner builds on that interface and owns transport and network policy. Spinner
is a plugin host: each host binary is a composition root that registers the
guest engines it ships with, or loads them at runtime. The standalone host's
static set is `pkg/spinner/cmd/gateway/plugins.txt`.
JavaScript, Lua, Shell, Go, and HookScript are separate language extensions.
JavaScript, Lua, and WebhookScript can also shape synchronous credential-source
requests for application-managed mail connections.

```text
JavaScript / Lua / Shell: public event -> Laravel route plan -> installed Go extension -> response / delivery queue
HookScript: public event -> Laravel route plan -> Go extension -> declared PHP companion -> Go network I/O
```

## Optional Go language extensions

- [`pkg/javascript`](../pkg/javascript/README.md) adds `javascript` using the
  pinned QuickJS-NG adapter.
- [`pkg/lua`](../pkg/lua/README.md) adds `lua` using gopher-lua without
  standard host libraries.
- [`pkg/shell`](../pkg/shell/README.md) adds `shell` as an opt-in local or SSH-backed hook
  executor rooted to one configured directory.
- [`pkg/hookscript`](../pkg/hookscript/README.md) adds `hookscript`; its Go
  guest calls the extension's enabled PHP/Primi companion through Spinner's
  declared reverse-call interface. Planner may host that companion statically
  through Composer or dynamically as an ext process.

Engines are never imported by Spinner. Select the static guests for the
standalone host by editing `pkg/spinner/cmd/gateway/plugins.txt`, then
regenerate the composition map:

```bash
# add or remove a line: <language> <go import path>
$EDITOR pkg/spinner/cmd/gateway/plugins.txt
go generate ./pkg/spinner/cmd/gateway/...
make build
```

Guests that are not compiled in can still be loaded at runtime:

```bash
export GATEWAY_PLUGIN_DIR_NATIVE=/opt/gateway/plugins/native   # .so guests
export GATEWAY_PLUGIN_DIR=/opt/gateway/plugins                # ext plugin store
```

Bundled Router binaries are separate composition roots. The default standalone
Spinner includes JavaScript, Lua, Shell, and Go but installs HookScript from its
combined extension release. Existing bundled Router compositions still carry
the HookScript Go guest and use Planner for its PHP companion. A route plan
whose language is not registered fails closed with HTTP 502; Gateway never
falls back to evaluating source in another runtime.

For static mode, install the matching Planner companion package in the Laravel
application:

```bash
composer require webong/gateway-javascript webong/gateway-lua
composer require webong/gateway-shell
```

In this monorepo, the root development project and bundled Router host use
Composer Merge Plugin to discover `pkg/*/composer.json`. Each extension owns
its PHP autoload rules, dependencies, and a tiny autoload registration file;
the root package does not duplicate extension namespaces or provider names.
Installing a published extension package continues to work through ordinary
Composer and Laravel package discovery.

For dynamic mode, publish a reviewed `ext.package/v1` release as a
gzip-compressed tar archive over HTTPS and record its URL, version and SHA-256
in the `/extensions` resource.
The package may carry both the Spinner guest entrypoint and a `planner`
entrypoint using the `php-jsonline` runtime. Its top-level descriptor describes
the Spinner process; `metadata["gateway.companion/v1"].descriptor` describes
the PHP process with the same package identity. Each process therefore exposes
only the contracts it actually serves. Planner pulls into
`GATEWAY_COMPANION_PLUGIN_DIR` and selects `planner`; Spinner pulls into its
separate `GATEWAY_PLUGIN_DIR` and selects `spinner`. Named packages without the
entrypoint for the current plane are rejected. The Planner extension resource
selects `static` or `dynamic`; it never runs both and never silently falls back
between them.

Planner does not contain a built-in language list. Each extension's Laravel
provider registers its execution target and route, schedule, or credential
capabilities. A
framework-free `ExtensionProviderRegistry` in `mod/adapter` carries the
Composer-discovered provider class names into Planner, while the
`AutomationLanguageRegistry` carries their capabilities. Spinner independently
advertises the engines present in the running binary with each plan and
schedule-claim request. Planner therefore fails closed unless both the
companion package and the matching Go engine are present.

`GATEWAY_AUTOMATION_LANGUAGES` remains an explicit deployment-policy fallback
for a third-party Spinner extension that has not shipped a Planner companion.
It registers the listed languages as Spinner-executed route and schedule
capabilities, but Spinner must still advertise the engine at runtime.

The standard Spinner image contains no PHP runtime. HookScript invokes its
trusted PHP companion in Planner, while JavaScript, Lua, Shell, and Go execute
through their installed Spinner guests. A custom composition root may add
other engines and their runtime dependencies explicitly:

```bash
docker build \
  -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-with-scripts .
```

A valid route plan whose language is not installed fails closed with
HTTP 502; Gateway never falls back to evaluating source in another runtime.
Laravel selects the language and source in its `RoutePlan`; the installed Go
extension owns compilation, execution limits, and its script capability
surface.

Third-party Go language extensions implement `automation.Engine` from
`github.com/webong/gateway/driver/automation`. They depend only on the
standard-library Driver module, not on Spinner. Their optional PHP companion
depends on the framework-free interfaces under `mod/adapter`, not Planner's
implementation source. A composition root (for example
`pkg/spinner/cmd/gateway`) calls `automationextensions.Register` with the guest
engine; Spinner itself does not import extension modules. External guests can
also be discovered from `GATEWAY_PLUGIN_DIR_NATIVE` (`.so`) or
`GATEWAY_PLUGIN_DIR` (ext plugin store) at runtime.

```php
use Webong\Gateway\Protocol\Automation;
use Webong\Gateway\Protocol\RoutePlan;

return RoutePlan::automate(new Automation(
    language: Automation::JAVASCRIPT,
    source: <<<'JS'
        gateway.forward({
          url: 'https://crm.example.test/webhooks/paid',
          method: 'POST',
          body: gateway.event.body,
        });
        gateway.respond('accepted', 202);
        JS,
));
```

JavaScript and Lua receive the same narrow interface:

- `gateway.event` — immutable ingress data: delivery ID, method, host, path,
  query, headers, and body.
- `gateway.get(path)` / `gateway.set(path, value)` — per-run variables.
- `gateway.respond(body, status)` — completes an HTTP request.
- `gateway.forward({ url, method, headers, body })` — adds an asynchronous HTTP
  delivery.
- `gateway.schedule({ key, name, cron, timezone, language, source, payload,
  enabled })` — declaratively creates or updates a Planner-owned schedule.

Lua uses that interface as a global table:

```lua
gateway.set("request_path", gateway.event.path)
gateway.respond("ok", 200)
```

## Optional WebhookScript extension

WebhookScript is deliberately not built into Planner. An application can
provide its companion statically through Composer:

```bash
composer require webong/gateway-hookscript
```

Composer/Laravel package discovery registers its provider and the trusted
`gateway.hookscript.execute` companion. In the bundled Router, Composer Merge
Plugin performs the same discovery from the extension-owned manifest.
Alternatively, install the HookScript ext package and select `dynamic` mode in
Planner; its `planner` entrypoint exposes the same action over
`ext.plugin/v1`. The activation record is the single switch, so discovering
both candidates cannot double-register or double-execute the action. A
`hookscript` plan is dispatched to the Go guest; that guest can invoke only the
declared PHP/Primi action and converts its result into an ordinary automation
outcome. Go remains responsible for ingress, egress, and outcome validation.

```php
return RoutePlan::automate(new Automation(
    language: Automation::WEBHOOKSCRIPT,
    source: <<<'PRIMI'
        if (var('request.header.x-event') == 'paid') {
            forward('https://crm.example.test/webhooks/paid', var('request.content'));
        }
        respond('accepted', 202);
        PRIMI,
));
```

The extension supports Webhook.site-style Primi syntax and a bounded Gateway
host interface. It does not copy Webhook.site product features such as stored
data or unrestricted HTTP access. See
[`pkg/hookscript/README.md`](../pkg/hookscript/README.md) for its complete
capability list, limits, configuration, and runtime requirements.

Without the extension, Go rejects `hookscript` route plans instead of
running a partial compatibility parser.

## Security seam

Scripts cannot access files, processes, packages, databases, raw sockets, or
arbitrary HTTP clients. Forwarding returns a delivery plan to Go; the router
then applies Gateway's URL validation, SSRF protections, timeouts, queue
capacity, retries, and audit behavior.

The JavaScript extension runs QuickJS per request with extension-owned memory,
stack, and execution-time limits. The Lua extension receives a deadline-bound
execution context. Core validates the common response and delivery outcome
before network I/O. The WebhookScript extension applies its own PHP-side
source, time, delivery, and log limits.
Keep scripts short; use normal workers for long-running or CPU-heavy work.

Ingress scripts execute through the common `automation.Executor` contract.
Spinner selects its local implementation with `GATEWAY_AUTOMATION_EXECUTOR`:
`ergo` (the default), `goroutine`, or `direct`. Ergo and goroutine modes use the
same bounded worker, queue, deadline, cancellation, and error contract; direct
mode is intended for debugging and delegates execution without a Spinner pool.
Trusted Planner route policy may set `automation.executor` to one of those
values for an individual event. When it is absent, Spinner uses the environment
default. The inbound request cannot select an executor directly.

The selected bounded pool waits for an
available worker within the request's total automation deadline, including time
already spent in its bounded queue. Client cancellation also cancels queued or
running work. When the worker and queue capacity remain exhausted until that
deadline, Gateway returns `503 Service Unavailable` with `Retry-After: 1`;
execution that starts but exceeds the deadline returns `504 Gateway Timeout`.
Configure the pool with `GATEWAY_AUTOMATION_WORKERS` (default `8`),
`GATEWAY_AUTOMATION_QUEUE_SIZE` (default `32`), and
`GATEWAY_AUTOMATION_TIMEOUT` (default `10s`). In Ergo mode, the node has
networking disabled. Streaming Shell responses retain their direct streaming
lifecycle because their HTTP response begins before execution completes.

## Credential hooks

An account can register an `email.credential-source` with adapter `script`, an
installed language, source code, a fixed HTTPS URL, and a write-only bearer.
This is the generic integration seam for applications whose credential API
uses a different method or JSON envelope than Gateway's built-in HTTP source.

JavaScript and Lua receive a `gateway.event` containing `account_id`,
`connection_id`, `protocol`, `address`, and `source_url`. WebhookScript receives
the same values as `credential.account_id`, `credential.connection_id`, and so
on. The script must emit exactly one delivery and cannot respond or schedule.

The script only shapes the request. Gateway requires its URL to equal the
registered source URL, permits POST, PUT, or PATCH, rejects authorization,
cookie, proxy, and host headers from the script, then injects the registered
bearer and performs the bounded HTTPS request. The script never sees either
the source bearer or the short-lived access token returned by the application.
The ordinary mailbox grant validator still verifies account, connection,
address, capability, and expiry before the token can be used.

## Scheduled automations

Schedules can run an installed Go-side language extension without an inbound
webhook. Laravel owns cron definitions and run history; Go claims due runs
through the private planner connection, dispatches them through the same
extension registry, queues their HTTP deliveries, and reports the outcome.
Spinner dispatches each claimed batch through a local Ergo actor pool with five
workers. The Ergo node has networking disabled: Planner's lease protocol still
coordinates work across Spinner nodes. Each actor owns one script execution at
a time, and the pool replaces a worker that stops. A script interrupted by an
actor crash is left claimed for Planner's existing lease recovery; it is not
immediately rerun because its side effects may already have happened. Actor
execution is bounded to 110 seconds within the two-minute claim lease.
HookScript schedules execute through the same Go guest and declared PHP
companion. A HookScript route may also create a schedule for any installed
Spinner-side language with `schedule([...])`.

The schedule API accepts only language identifiers registered with the
`schedule` capability. A polling Spinner sends its active engine catalog when
claiming work, and Planner claims only schedules that worker can execute.
Additional runtime extensions therefore require no Planner source changes and
mixed worker builds cannot accidentally claim unsupported jobs.

A schedule's `gateway.event` contains `type: "schedule"`, `run_id`,
`schedule_id`, `scheduled_for`, and the configured `payload`.
`gateway.get('payload.key')` also reads that payload.

Enable polling on separate Spinner with `GATEWAY_RUNTIME=http`,
`GATEWAY_SCHEDULES_ENABLED=true`, `GATEWAY_PLANNER_URL`, and a shared
`GATEWAY_INTERNAL_TOKEN`. The bundled RoadRunner and FrankenPHP-library Router
also support `GATEWAY_SCHEDULES_ENABLED=true`; they call their embedded Planner
through the same private schedule endpoints without another network listener.
The optional `GATEWAY_SCHEDULE_POLL_INTERVAL` defaults to `5s`. Run the package
migrations before enabling it. All Planner workers must share one database, and
each polling Router advertises its installed languages so a worker only claims
runs it can execute. Schedule management uses the existing `REGISTRY_TOKEN`
bearer token and should be exposed only through an authenticated control-plane
route.

```http
POST /schedules
Authorization: Bearer <REGISTRY_TOKEN>
Content-Type: application/json

{
  "name": "Minute check",
  "cron": "* * * * *",
  "timezone": "UTC",
  "language": "javascript",
  "source": "gateway.forward({url: 'https://example.test/check', method: 'POST', body: JSON.stringify(gateway.event.payload)});",
  "payload": {"check": "upstream"},
  "enabled": true
}
```

Use `GET /schedules`, `GET /schedules/{id}`, `PATCH /schedules/{id}`,
`DELETE /schedules/{id}`, `GET /schedules/{id}/runs`, and
`POST /schedules/{id}/runs` with the same bearer token. The POST queues a
manual run and returns `202` with a `pending` run resource; an installed Spinner
worker claims it through the ordinary lease protocol. A manual run snapshots
the schedule's language, source, and payload when queued, can run even while
the cron schedule is paused, and does not change its next cron time. Supply an
optional `Idempotency-Key` (1–128 letters, digits, dots, underscores, colons, or
hyphens) to make repeated POSTs return the same run instead of queueing
duplicates. Cron uses five fields and the named timezone. Missed intervals coalesce
into one occurrence after downtime; there is no catch-up storm. Each run has a
two-minute claim lease, and an expired claim can be taken by another router.

Run history reports script failure or the count **accepted for HTTP delivery**,
not successful downstream delivery. With `GATEWAY_DELIVERY_SPOOL_PATH`, accepted
deliveries are durable and retried by Spinner; without it they use the
in-process worker queue. If a router crashes after enqueueing but before
reporting, reclaiming can queue duplicates; consumers should use
`gateway.event.run_id` as an idempotency key when building deliveries. A scheduled
`gateway.forward({ url, expected_status: 204 })` asserts an exact HTTP response
status. `expected_body_sha256` can also assert the lowercase SHA-256 digest of
the complete response body. Either assertion requires
`GATEWAY_DELIVERY_SPOOL_PATH`; without a durable spool the run fails at queue
handoff. A mismatch is a terminal failed delivery. The receipt records expected
and observed statuses plus `body_assertion: matched|mismatched`, but never the
response body or headers. Body inspection is bounded by `MAX_BODY_SIZE`;
an oversized response fails the asserted delivery rather than being accepted
as a partial match.
Each run resource now includes `deliveries_url`, which filters the delivery
management API by that run ID. On a durable spool it shows the queued,
deferred, failed, or delivered receipts for callbacks emitted by that run.
This is an inspection link, not a second success flag: `status: succeeded`
still means script execution and queue handoff succeeded. A volatile worker
handoff has no receipt, and the management API requires a configured private
Spinner delivery-admin connection.
The run also includes `outcome_url` (`GET
/schedules/{schedule}/runs/{run}/outcome`). This registry-authenticated,
read-only view keeps `execution_status` separate from `delivery_status` and
returns receipt counts across configured Spinner nodes. Delivery status is
`pending` while callbacks are queued or deferred, `failed` when a terminal
failure is known, `delivered` only when all expected receipts are present and
delivered, `none` when the completed run queued no callbacks, or `unknown`
when receipts are missing or a node's 100-item response may be truncated. A
configured node that cannot be checked returns 503, not a false success.
Reclaimed runs may queue duplicates; `deliveries_observed` can therefore exceed
`deliveries_queued`. Such an overcount is `unknown`, not proof that every
intended callback succeeded. This view reflects status and body assertion failures
through failed delivery receipts.

### Operator notifications

For a Planner-managed failed-run alert, deploy an exact HTTPS receiver hostname
with `GATEWAY_SCHEDULE_NOTIFICATION_ALLOWED_HOSTS=ops.example.test`, then create
the singleton destination:

```sh
curl -X POST https://planner.example.test/schedules/notification-destination \
  -H "Authorization: Bearer $REGISTRY_TOKEN" -H 'Content-Type: application/json' \
  -d '{"url":"https://ops.example.test/gateway-alerts","token":"receiver-secret"}'
```

The authenticated `GET` returns the URL, enabled flag, revision, and whether a
token is set, never the token itself. `PUT` replaces the destination (omitting
`token` keeps the existing secret), and `DELETE` removes it. Use the ETag from
`POST`, `GET`, or `PUT` as `If-Match` for changes. A disabled or deleted resource
cannot enable an env destination. The deployed host list is checked
again before dispatch, so removing a hostname stops delivery without deleting
the resource. Scripts cannot select a receiver. Redirects are not followed.

The former `GATEWAY_SCHEDULE_NOTIFICATION_URL` and
`GATEWAY_SCHEDULE_NOTIFICATION_TOKEN` settings are no longer read. Migrate
their values into the Planner resource before upgrading a live deployment.
HTTP is accepted only for an explicitly allowed `127.0.0.1` receiver, for
local smoke tests; use HTTPS for externally named receivers.

Completed runs enter the `schedule_notifications` outbox. A script or queue
handoff failure is alertable immediately. For a successfully executed run,
Planner waits for durable delivery receipts and alerts only when one reaches a
terminal `failed` state. `pending` and `unknown` outcomes are checked again;
fully delivered or no-delivery runs become `resolved` without an alert. A
delivery failure cannot be detected from a volatile in-process worker queue:
configure `GATEWAY_DELIVERY_SPOOL_PATH` and the private delivery-admin
connection on every Spinner node for downstream alerts.

When `GATEWAY_SCHEDULES_ENABLED=true`, Spinner automatically asks Planner to
dispatch due alerts through the authenticated private schedule connection.
The polling cadence is at least five seconds. Operators can also run
`php artisan gateway:send-schedule-notifications` (for example from a Laravel
scheduler or cron) if Spinner polling is not running. Failed handoffs retry
with bounded backoff; the run list exposes `notification_status`,
`notification_attempts`, and `notification_next_attempt_at`. Notifications
are created only for runs completed after the URL is configured, so enabling
the feature does not send an unexpected historical backlog.

The webhook has event `schedule.run.failed`, a stable `notification_id` and
`Idempotency-Key: gateway-schedule-run-{run_id}`, the schedule/run IDs, a
relative `outcome_url`, and the same metadata-only `outcome` view. It excludes
script source, run payload, callback URLs, response bodies, and error text.
Delivery is at least once: a crash after receiver acceptance but before the
outbox is marked `sent` can replay the same alert. Receivers should deduplicate
by the idempotency key.

### Creating schedules from scripts

Scripts emit schedule commands; they never receive database access. Planner
validates and persists each command. `key` is required and is idempotent within
the script origin: executing the same route again updates that schedule instead
of inserting a duplicate. Route schedules use the matched endpoint as their
origin; schedules emitted by a scheduled run use the parent schedule. One run
may emit at most 8 commands and one origin may own at most 32 schedules.

The target `language` must be registered for Spinner-side scheduled execution.
For JavaScript and Lua route scripts, both the HTTP runtime and bundled Router
forward commands to the same private Planner endpoint. The bundled Router uses
its in-process handler rather than an external connection.

From the repository checkout, `make test-schedule-roadrunner` builds the
bundled Router with Lua, starts a temporary Laravel/SQLite Planner, and
verifies both a due run and a child schedule registered by that run.

JavaScript:

```javascript
gateway.schedule({
  key: 'daily-report',
  name: 'Daily report',
  cron: '0 9 * * *',
  timezone: 'Africa/Lagos',
  language: 'javascript',
  source: "gateway.forward({url: 'https://example.test/report', method: 'POST'})",
  payload: {team: 'ops'},
  enabled: true,
});
```

Lua:

```lua
gateway.schedule({
  key = "daily-report",
  name = "Daily report",
  cron = "0 9 * * *",
  timezone = "Africa/Lagos",
  language = "lua",
  source = "gateway.forward({url = 'https://example.test/report', method = 'POST'})",
  payload = {team = "ops"},
  enabled = true
})
```

WebhookScript / Primi:

```php
schedule([
  'key': 'daily-report',
  'name': 'Daily report',
  'cron': '0 9 * * *',
  'timezone': 'Africa/Lagos',
  'language': 'javascript',
  'source': "gateway.forward({url: 'https://example.test/report', method: 'POST'})",
  'payload': ['team': 'ops'],
  'enabled': true
]);
```

Omit `timezone`, `payload`, or `enabled` to use `UTC`, an empty payload, and
enabled respectively. Changing a command's `key` creates another schedule;
use the schedule management API to inspect, disable, or delete old keys.
