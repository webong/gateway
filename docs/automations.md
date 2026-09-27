# Gateway Automations

Gateway Automations run small, bounded ingress logic after Laravel has matched
and authorized a route. Core Gateway executes JavaScript and Lua in Go. The
optional `webong/gateway-webhookscript` extension executes WebhookScript in PHP
with Primi before the plan crosses the bridge.

```text
JavaScript / Lua: public event -> Laravel route plan -> Go sandbox -> response / delivery queue
WebhookScript:    public event -> Laravel + optional Primi extension -> ordinary route plan -> Go network I/O
```

## Core languages

- `javascript` runs in the pinned QuickJS-NG engine supplied by the
  `github.com/buke/quickjs-go` Go module through Gateway's small adapter.
- `lua` runs in the embedded Lua runtime.

Laravel selects a core automation in its `RoutePlan`; Go owns compilation,
execution, timeouts, memory limits, and the script capability surface.

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

Laravel package discovery registers its provider and decorates the configured
`RoutePlanner`. A `webhookscript` plan is evaluated with Primi 0.4 in PHP and
converted into an ordinary Gateway response/relay plan. Go therefore remains
responsible only for ingress and egress network traffic.

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

QuickJS runs per request with router-owned memory, stack, and execution-time
limits. Lua receives a deadline-bound execution context. The WebhookScript
extension applies its own PHP-side source, time, delivery, and log limits.
Keep scripts short; use normal workers for long-running or CPU-heavy work.

## Scheduled automations

Schedules run Go-side `javascript` or `lua` scripts without an inbound webhook.
Laravel owns cron definitions and run history; Go claims due runs through the
private planner connection, executes scripts, queues their HTTP deliveries,
and reports the outcome. The optional WebhookScript planner extension does not
alter scheduled execution.

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
