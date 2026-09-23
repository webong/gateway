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

- `javascript` runs in the pinned QuickJS-NG engine through Gateway's small
  CGO bridge.
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
limits. Lua receives a deadline-bound execution context. Go builds require a C
compiler and CGO; the production container builds the pinned engine from
source. Keep scripts short; use normal worker services for long-running or
CPU-heavy work.
