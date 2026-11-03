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

The Planner's existing subscription storage still uses `webhook_group` and
`webong/proxy` records. Those persisted fields remain compatible and are not a
signal that every HTTP request is a Webhook. A later storage migration can
rename the generic group field without changing route-kind dispatch.
