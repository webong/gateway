# ADR 0011: Separate HTTP ingress from Webhook routes

## Decision

HTTP is a core Planner protocol and the `http` subscription target is a core
Planner capability. The Web extension owns Webhook routing and registers the
`webhook` route kind. A path binding selects its route kind; existing bindings
default to `webhook` for compatibility. A binding with kind `http` uses the
core registry planner for subscription routing and HTTP targets. An unbound
path passes the original request through to the configured HTTP host.
An installed route kind with no active owner fails closed. WebSocket and
Webstream remain Web extension protocols.

Webhook is a route kind over HTTP, not a second wire protocol. Core Planner
owns endpoint lookup, subscription matching, fan-out, and HTTP target
validation. Web registers the Webhook route kind and owns its event recording
and policy. Other route kinds can use HTTP ingress without inheriting Webhook
observation behavior.

## Consequences

Planner exposes `group` in its subscription API. The current `webong/proxy`
storage adapter still writes that value to its `webhook_group` column; the
storage name is never exposed as the public subscription field. Planner stores
endpoint `route_kind` in existing endpoint metadata and exposes it through
`/registry/endpoints` management, so no route-kind table is required.
