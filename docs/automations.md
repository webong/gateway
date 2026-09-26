# Gateway Automations

Gateway Automations run small, bounded logic at the Go router after the Laravel
planner has matched and authorized an ingress route. They are useful for
webhook transforms, simple provider verification, filtering, and forwarding.

```text
public event -> Laravel route plan -> Go script sandbox -> response / delivery queue
```

Laravel selects an automation in its `RoutePlan`; it does not execute the
source. The Go router owns compilation, execution, timeouts, memory limits,
and all script capabilities.

## Languages

- `javascript` runs in the pinned QuickJS-NG engine supplied by the
  `github.com/buke/quickjs-go` Go module through Gateway's small adapter.
- `lua` runs in the embedded Lua runtime.
- `webhookscript` is Gateway's compatibility dialect for WebhookScript-style
  `var`, `set`, and `respond` flows. It compiles to the restricted JavaScript
  capability surface; it is not a copy of Webhook.site's database, filesystem,
  or unrestricted network API.

The Laravel planner returns an automation plan:

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

## Capability API

All languages receive the same narrow API:

- `gateway.event` — immutable ingress data: delivery ID, method, host, path,
  query, headers, and body.
- `gateway.get(path)` / `gateway.set(path, value)` — per-run variables.
- `gateway.respond(body, status)` — completes an HTTP request.
- `gateway.forward({ url, method, headers, body })` — adds an asynchronous HTTP
  delivery.

Lua uses the same API as a global table:

```lua
gateway.set("request_path", gateway.event.path)
gateway.respond("ok", 200)
```

WebhookScript-style code uses `var`, `set`, and `respond`:

```text
set('request.path_copy', var('request.path'));
respond('accepted', 202);
```

## Security boundary

Scripts cannot access files, processes, packages, databases, raw sockets, or
arbitrary HTTP clients. `gateway.forward` returns a delivery to the Go router;
the router then applies Gateway's existing URL validation, SSRF protections,
timeouts, queue capacity, retries, and audit behavior.

QuickJS runs per request with router-owned memory, stack, and execution-time
limits. Lua receives a deadline-bound execution context. Go builds still require
a C compiler and CGO because the dependency compiles QuickJS-NG; Gateway no
longer carries the engine source. Keep scripts short; use normal worker services
for long-running or CPU-heavy work.

## Scheduled automations

Schedules run the same Go-side `javascript`, `lua`, or `webhookscript` scripts
without an inbound webhook. Laravel owns cron definitions and run history; Go
claims due runs through the private planner connection, executes scripts, queues
their HTTP deliveries, and reports the outcome. A schedule's `gateway.event`
contains `type: "schedule"`, `run_id`, `schedule_id`, `scheduled_for`, and the
configured `payload`. `gateway.get('payload.key')` also reads that payload.

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
