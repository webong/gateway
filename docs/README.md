# Gateway documentation

Gateway Router is one product with two modules:

- **Spinner (Go):** public listeners, protocol parsing, delivery execution,
  retries, and outbound SSRF protection.
- **Planner (PHP):** authentication, endpoint and subscription
  management, matching, route planning, and managed-server state.

The modules can be deployed separately, with Spinner calling Planner over a
private connection, or together in the Gateway Router distribution using
embedded RoadRunner or FrankenPHP. Both use the same planning contract.

## Guides

- [Gateway Router](router.md): run Spinner and the bundled Planner together,
  natively or in the optional FrankenPHP image.
- [Gateway Extensions in Planner](planner-extensions.md): assemble Web, DNS,
  Email, and Database Planner entrypoints without coupling them to Planner core.
- [Distributed getting started](getting-started.md): install the Planner in a
  Laravel app and run a standalone Spinner.
- [Deployment](deployment.md): run Spinner with a private Planner and TLS.
- [Kubernetes deployments](kubernetes.md): combined Router or separate Spinner and standalone Planner images, plus provisioning-worker manifests and opt-in Email, DNS, and TLS edge overlays.
- [Deployment targets](provisioning-targets.md): run Docker or Kubernetes workload workers for managed realtime services.
- [DNS high availability](dns-ha.md): deploy and verify two authoritative Spinner nodes and parent-zone delegation.
- [Delivery operations](deliveries.md): inspect a durable spool and retry failed deliveries through a private listener.
- [Endpoint event history](events.md): opt-in Web ingress records and shared DNS history with bounded capture.
- [Email authentication gate](email-authentication.md): SPF/DKIM/DMARC, SRS, and bounce-routing requirements before public forwarding.
- [Mail forwarding and retrieval](mail-forwarding.md): SMTP routing, durable relay, and the TLS POP3 upstream proxy.
- [Email delivery-status webhooks](email-delivery-status-webhooks.md): account-scoped signed terminal delivery events with durable retries.
- [Subscription matching](subscription-dsl-v1.md): versioned matching rules.
- [Subscription examples](examples/subscriptions/README.md): direct PHP and
  remote HTTP registration, including endpoint network profiles.
- [Gateway Automations](automations.md): optional JavaScript, Lua, Shell, and
  PHP WebhookScript language extensions.

## Capabilities

| Protocol or service | Spinner (Go) | Planner (PHP) |
| --- | --- | --- |
| HTTP/webhooks | Capture traffic; execute replies and deliveries | Authorize, match, choose destinations, and optionally record ingress history |
| WebSocket | Upgrade and session lifecycle | Accept, reject, or deliver events |
| Email | SMTP/TLS ingress, outbound relay, and TLS POP3/IMAP upstream proxies | SMTP decisions, verification-gated domains/aliases, and optional mailbox authentication |
| DNS | Authoritative UDP/TCP service | Zone and hook decisions |
| Automations | Advertise and dispatch installed engines; enforce delivery policy | Authorize and select source; register companion capabilities; optionally execute Planner-local languages |
| Realtime | Route managed workloads | Reverb, Mercure, Centrifugo desired state |

## Public and private surfaces

Public management APIs include `/registry/endpoints`, endpoint subscriptions,
`/servers`, `/applications`, and `/deliveries`; they use the registry bearer
token. `/deliveries` requires a private Spinner admin connection.

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
