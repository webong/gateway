# Gateway outbound transport pools

Gateway keeps the customer submission request unchanged. Zorvia submits one authenticated message to `POST /email/submissions` with the existing `mail_from`, `recipients`, `message_base64`, `Idempotency-Key`, and optional shared-sender `sender_name` and `reply_route_id`. It does not name a transport, provider, or pool. The visible shared From and conversation Reply-To are still rewritten before submission. The accepted response retains `id`, `state`, `node`, and `delivery_url`, and adds `status_url: "/email/submissions/{id}"`. The same HTTP Basic credential can poll `GET` at that URL for an account-scoped, provider-neutral state and attempt count; another sender identity receives 404. Gateway retains that status ownership for 30 days.

## Operator configuration

All management endpoints require HTTPS and the Gateway registry bearer token. Create a transport with `POST /email/outbound-transports`:

```json
{
  "name": "primary-smtp",
  "type": "smtp",
  "address": "smtp.provider.example:587",
  "tls_mode": "starttls",
  "username": "gateway",
  "password": "operator-supplied-secret",
  "authorized_domains": ["operator.example"],
  "domain_readiness": {
    "operator.example": {"spf": "ready", "dkim": "ready"}
  },
  "healthy": true,
  "rate_limit_per_minute": 120
}
```

SMTP transport metadata is an encrypted Planner resource. Its password is stored in a separate encrypted credential resource. `GET /email/outbound-transports` and `GET /email/outbound-transports/{id}` omit both the password and credential reference. `PATCH /email/outbound-transports/{id}` accepts one of `enabled`, `healthy`, `domain_readiness: {"domain":"...","status":{"spf":"ready|pending|not_required","dkim":"ready|pending|not_required"}}`, or `domain_authorization` with the same domain and status shape. `domain_authorization` atomically adds the generic sender domain authorization and its initial readiness; it is idempotent for an already-authorized domain. An operator attests provider authorization and SPF/DKIM readiness; Gateway cannot infer third-party authorization from the customer's DNS alone. A pending status excludes the transport. `POST /email/outbound-transports/{id}/credential` replaces the stored password and returns only `{"rotated":true}`. SMTP transports use TLS and Gateway's public-network mail dial policy.

The application owns any Resend, SES, or other provider control-plane adapter.
That adapter registers and verifies the customer domain with the provider before
it marks the generic transport/domain readiness here. Gateway owns proxying,
credential protection, queueing, selection, retries, signing, and readiness
enforcement; it neither stores provider domain identifiers nor calls provider
domain-management APIs.

Create a pool with `POST /email/outbound-pools`:

```json
{
  "name": "shared-sender",
  "members": [
    {"transport_id": "<primary UUID>", "priority": 0, "weight": 3},
    {"transport_id": "<backup UUID>", "priority": 1, "weight": 1}
  ]
}
```

Equal-priority members are selected by a deterministic weighted hash of the delivery ID. A higher numeric priority is a backup. The binding API checks every member's account and sender-domain authorization. Use `POST /email/outbound-bindings` with `account_id`, `scope` (`shared_route`, `managed_route`, or `domain`), `sender_id`, and `pool_id`. `shared_route` and `managed_route` take a sending-route UUID; `domain` takes a sending-domain UUID. A `managed_route` binding is authoritative for that route: when enabled it selects its pool, and when disabled it stops selection rather than silently using the domain pool. A route with no exact binding falls back to the domain binding. Shared-route behavior is unchanged. `GET` lists pools and bindings. `PATCH /email/outbound-pools/{id}` accepts `{"enabled":false}` to stop new selection. `PATCH /email/outbound-bindings/{id}` accepts either `{"enabled":false}` or `{"pool_id":"<pool UUID>"}`; pool reassignment repeats the same enabled-pool, account, verified-domain, and member-authorization checks as creation. `GET /email/outbound-attempts` shows selected transport IDs and normalized states to operators, without credentials.

For safe setup ordering, an account may create a managed sending route with `POST /email/mailbox-accounts/{account}/sending-routes` and `{"domain_id":"<domain UUID>","local_part":"support","enabled":false}`. Gateway returns the normal route and SMTP credential, but rejects authentication while it is disabled. The operator can then create its `managed_route` binding and finally enable the route with `PATCH /email/mailbox-accounts/{account}/sending-routes/{route}` and `{"enabled":true}`. Binding a disabled route is allowed only while its account-owned sending domain remains verified, enabled, and DNS-ready.

A transport or binding is opt-in. The installable Email Extension advertises `managed.smtp` and `mailbox.smtp`, plus the `smtp` adapter when revisioned `smtp_relay` configuration is present. Generic hosts do not parse an Email relay or resolver environment variable. The managed adapters resolve their current plans, capacity reservations, DKIM keys, and mailbox credentials through Email's identity-scoped private Planner routes; an unavailable or incompatible lookup fails closed. A durable delivery spool or database is required in every host.

## Delivery semantics

The queue gives each managed delivery a durable ID. Planner pins its ordered, eligible transport IDs under that ID in the shared encrypted control plane, so retries and replicas read the same order. Each send reserves shared per-minute capacity atomically. Gateway tries the next candidate only after a definite pre-accept connection failure or SMTP 421, 450, 451, or 452. An SMTP 5xx, authentication failure, or ambiguous post-DATA acknowledgement stops failover. One message is sent to one transport at a time. The existing queue idempotency key and SMTP uncertainty rule remain in force. An SMTP relay's successful DATA response is recorded as `accepted`; final recipient delivery requires a later provider event and is not inferred from that response.

Operator attempt states include `queued`, `attempted`, `accepted`, `temporarily_delayed`, `delivered`, `bounced`, `rejected`, `complained`, and `ambiguous`. The first release has an internal outcome recording endpoint for provider adapters. A provider's later delivery, bounce, or complaint notification must be integrated by that adapter before those later states can be asserted. Treat transport health and domain readiness as operator-maintained until a real provider check is connected.
