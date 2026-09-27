# Gateway Automations

Gateway Automations run small, bounded ingress logic after Laravel has matched
and authorized a route. The standalone Gateway Driver module owns the
language-neutral execution interface, dispatcher, and output validation.
Spinner builds on that interface and owns composition and network policy.
JavaScript, Lua, and WebhookScript are separate language extensions.

```text
JavaScript / Lua: public event -> Laravel route plan -> installed Go extension -> response / delivery queue
WebhookScript:    public event -> Laravel + optional Primi extension -> ordinary route plan -> Go network I/O
```

## Optional Go language extensions

- [`ext/javascript`](../ext/javascript/README.md) adds `javascript` using the
  pinned QuickJS-NG adapter.
- [`ext/lua`](../ext/lua/README.md) adds `lua` using gopher-lua without
  standard host libraries.

Neither interpreter is linked into a default Gateway build. Select the
extensions at the Go composition root with build tags:

```bash
# no script engines
make build

# QuickJS-NG and Lua
make build-with-script-extensions

# one engine only
make build GATEWAY_BUILD_TAGS=gateway_javascript
make build GATEWAY_BUILD_TAGS=gateway_lua
```

Install the matching Planner companion package in the Laravel application:

```bash
composer require webong/gateway-javascript webong/gateway-lua
```

In this monorepo, the root development project and bundled Router host use
Composer Merge Plugin to discover `ext/*/composer.json`. Each extension owns
its PHP autoload rules, dependencies, and a tiny autoload registration file;
the root package does not duplicate extension namespaces or provider names.
Installing a published extension package continues to work through ordinary
Composer and Laravel package discovery.

Planner does not contain a built-in language list. Each extension's Laravel
provider registers its execution target and route/schedule capabilities. A
framework-free `ExtensionProviderRegistry` in `lib/contract` carries the
Composer-discovered provider class names into Planner, while the
`AutomationLanguageRegistry` carries their capabilities. Spinner independently
advertises the engines present in the running binary with each plan and
schedule-claim request. Planner therefore fails closed unless both the
companion package and the matching Go engine are present.

`GATEWAY_AUTOMATION_LANGUAGES` remains an explicit deployment-policy fallback
for a third-party Spinner extension that has not shipped a Planner companion.
It registers the listed languages as Spinner-executed route and schedule
capabilities, but Spinner must still advertise the engine at runtime.

For a container image, pass the same selection as a build argument:

```bash
docker build \
  --build-arg GATEWAY_BUILD_TAGS=gateway_javascript,gateway_lua \
  -t gateway-with-scripts .
```

The same tags work with direct `go build` commands and the FrankenPHP image
build. A valid route plan whose language is not installed fails closed with
HTTP 502; Gateway never falls back to evaluating source in another runtime.
Laravel selects the language and source in its `RoutePlan`; the installed Go
extension owns compilation, execution limits, and its script capability
surface.

Third-party Go language extensions implement `automation.Engine` from
`github.com/webong/gateway/driver/automation`. They depend only on the
standard-library Driver module, not on Spinner. Their optional PHP companion
depends on the framework-free interfaces under `lib/contract`, not Planner's
implementation source. Spinner's composition root registers selected engines
with the Driver dispatcher.

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

WebhookScript is deliberately not bundled with the Planner. Once published or
configured as a Composer path repository, install the separate package when an
application needs it:

```bash
composer require webong/gateway-webhookscript
```

Composer/Laravel package discovery registers its provider and decorates the
configured `RoutePlanner`. In the bundled Router, Composer Merge Plugin performs
the same discovery from the extension-owned manifest. A `webhookscript` plan is
evaluated with Primi 0.4 in PHP and converted into an ordinary Gateway
response/relay plan. Go therefore remains responsible only for ingress and
egress network traffic.

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
[`ext/webhookscript/README.md`](../ext/webhookscript/README.md) for its complete
capability list, limits, configuration, and runtime requirements.

Without the extension, Go rejects `webhookscript` route plans instead of
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

## Scheduled automations

Schedules can run an installed Go-side language extension without an inbound
webhook. Laravel owns cron definitions and run history; Go claims due runs
through the private planner connection, dispatches them through the same
extension registry, queues their HTTP deliveries, and reports the outcome.
The optional WebhookScript planner extension does not execute scheduled jobs,
but a WebhookScript route may create a schedule for an installed Go-side
language with `schedule([...])`.

The schedule API accepts only language identifiers registered with the
`schedule` capability. A polling Spinner sends its active engine catalog when
claiming work, and Planner claims only schedules that worker can execute.
Additional runtime extensions therefore require no Planner source changes and
mixed worker builds cannot accidentally claim unsupported jobs.

A schedule's `gateway.event` contains `type: "schedule"`, `run_id`,
`schedule_id`, `scheduled_for`, and the configured `payload`.
`gateway.get('payload.key')` also reads that payload.

Enable polling on the Go router with `GATEWAY_RUNTIME=http`,
`GATEWAY_SCHEDULES_ENABLED=true`, `GATEWAY_LARAVEL_BACKEND_URL`, and a shared
`GATEWAY_INTERNAL_TOKEN`. The optional `GATEWAY_SCHEDULE_POLL_INTERVAL` defaults
to `5s`. Run the package migrations before enabling it. The Laravel planner
must be reachable privately by every Go router node, and all planner workers
must share one database. Schedule management uses the existing `REGISTRY_TOKEN`
bearer token and should be exposed only through an authenticated control-plane
route. This version does not run the poller in embedded RoadRunner mode.

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
`DELETE /schedules/{id}`, and `GET /schedules/{id}/runs` with the same bearer
token. Cron uses five fields and the named timezone. Missed intervals coalesce
into one occurrence after downtime; there is no catch-up storm. Each run has a
two-minute claim lease, and an expired claim can be taken by another router.

Run history reports script failure or the count **accepted by the in-process
HTTP queue**, not successful downstream delivery. If a router crashes after
queueing but before reporting, reclaiming can queue duplicates; consumers
should use `gateway.event.run_id` as an idempotency key when building deliveries.
This first version does not implement durable HTTP delivery, retry policies,
response assertions, notifications, or a manual-run endpoint.

### Creating schedules from scripts

Scripts emit schedule commands; they never receive database access. Planner
validates and persists each command. `key` is required and is idempotent within
the script origin: executing the same route again updates that schedule instead
of inserting a duplicate. Route schedules use the matched endpoint as their
origin; schedules emitted by a scheduled run use the parent schedule. One run
may emit at most 8 commands and one origin may own at most 32 schedules.

The target `language` must be registered for Spinner-side scheduled execution.
For JavaScript and Lua route scripts, the HTTP runtime forwards commands to the
private Planner endpoint. Embedded RoadRunner does not currently provide that
control-plane connection and fails the automation if it emits a schedule.

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
