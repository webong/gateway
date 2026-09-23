# Gateway documentation

Gateway is one product with two cooperating planes:

- **Go router plane:** public listeners, protocol parsing, delivery execution,
  retries, and outbound SSRF protection.
- **Laravel planner plane:** authentication, endpoint and subscription
  management, matching, route planning, and managed-server state.

The planner is a Gateway component, not an unrelated application behind a
proxy. Users call Gateway's public edge; the router calls the planner over a
private connection.

## Guides

- [Getting started](getting-started.md): install the Laravel planner, run the
  router, register endpoints, and create subscriptions.
- [Deployment](deployment.md): run Gateway in containers with TLS and private
  planner networking.
- [Subscription matching](subscription-dsl-v1.md): versioned matching rules.
- [Gateway Automations](automations.md): Go-executed WebhookScript, Lua, and
  JavaScript ingress logic.

## Capabilities

| Protocol or service | Go router | Laravel planner |
| --- | --- | --- |
| HTTP/webhooks | Capture traffic; execute replies and deliveries | Authorize, match, and choose destinations |
| WebSocket | Upgrade and session lifecycle | Accept, reject, or deliver events |
| SMTP | SMTP/TLS session handling | Recipient and authentication decisions |
| DNS | Authoritative UDP/TCP service | Zone and hook decisions |
| Automations | Execute bounded scripts; enforce delivery policy | Authorize and select script source |
| Realtime | Route managed workloads | Reverb, Mercure, Centrifugo desired state |

## Public and private surfaces

Public management APIs include `/registry/endpoints`, endpoint subscriptions,
`/servers`, and `/applications`; they use the registry bearer token.

`/_internal/gateway/plan`, `/_internal/gateway/event`, and provisioning APIs
are private. The router calls them with `GATEWAY_INTERNAL_TOKEN`; never expose
them through the public edge.

## Security boundary

- Terminate TLS at Caddy, a load balancer, or equivalent.
- Do not publish a planner port.
- Keep `REGISTRY_TOKEN` and `GATEWAY_INTERNAL_TOKEN` distinct.
- Production callback targets must be public HTTP(S) addresses. Gateway blocks
  loopback, Docker-private, link-local, and unspecified targets to prevent
  SSRF.
