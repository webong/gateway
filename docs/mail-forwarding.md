# Gateway mail forwarding plan

Gateway is a mail transport and routing plane. It accepts SMTP transactions and
asks the Laravel planner whether to accept, reject, or deliver them. The product
builds on that seam instead of embedding alias, tenant, or storage policy in
the Go listener.

## Product boundary

- Go owns SMTP sockets, TLS, message limits, outbound submission, timeouts, and
  the durable delivery queue.
- Laravel owns domains, ownership verification, aliases, catch-all and pattern
  matching, tenant limits, credentials, and delivery policy.
- Raw RFC 5322 messages cross the existing protocol event contract unchanged.
- Gateway does not own mailbox storage or indexing. It may expose IMAP and
  POP3 transport gateways backed by an external mailbox system.

The outbound mail module exposes one operation: submit an envelope and message
through a configured SMTP relay. The implementation hides connection setup,
TLS, authentication, and transaction lifecycle from callers.

## Extension seam

Every accepted message is an immutable envelope plus its raw RFC 5322 payload.
The durable delivery queue dispatches that value to registered delivery
adapters. SMTP forwarding is the first built-in durable adapter; existing HTTP
and webhook routes continue to use Gateway's HTTP worker path.
`GatewayDelivery.protocol` describes the message protocol, while the optional
`GatewayDelivery.adapter` selects a trusted implementation. When omitted, the
adapter defaults to the protocol name. Namespaced adapters such as
`vendor.mailbox` can therefore accept SMTP messages without pretending to be
an SMTP server.

A storage vendor or application can add another adapter without changing SMTP
ingress, alias resolution, retry scheduling, or the message contract. The
adapter owns storage-specific encryption, indexing, retention, and quotas.
Gateway only records whether the adapter durably accepted the delivery and
exposes the resulting delivery event.

Retrieval uses a separate upstream seam. The IMAP and POP3 listeners own public
TLS, protocol parsing, session limits, and client lifecycle. After the planner
authenticates and resolves the account, the listener connects to a trusted
mailbox upstream. The upstream owns mailbox state, flags, UIDs, folders,
search, retention, and message bodies.

The first retrieval adapter should be a transparent upstream proxy. This lets
Gateway provide custom-domain IMAP and POP3 endpoints without reimplementing
mailbox semantics. A later semantic mailbox adapter may translate IMAP and
POP3 operations for storage systems that do not expose those protocols, but it
must satisfy the same session contract and remain outside Gateway storage.

Unknown adapter types must be rejected during planning. An adapter must not be
loaded dynamically from an untrusted message or tenant setting; deployments
register trusted adapters explicitly.

## Delivery stages

### 1. Relay-backed forwarding

Use a configured SMTP relay for outbound delivery. Add typed SMTP destinations
to the protocol planner, a durable queue, retry state, and delivery events.
This produces useful custom-domain alias forwarding without making Gateway
responsible for public sending reputation on day one.

### 2. Mail control plane

Add managed mail domains and aliases, DNS ownership checks, exact and catch-all
routes, multiple recipients, pause/delete behavior, and an API. Keep these
records distinct from HTTP WebProxy destinations even if they reuse shared
endpoint and event infrastructure.

### 3. Forwarding correctness

Add spam and malware decisions, SPF and DMARC evaluation, SRS envelope
rewriting, DKIM verification, ARC sealing, loop detection, suppression lists,
bounces, and abuse controls. Do not advertise reliable internet forwarding
before these protections and retry persistence are deployed.

### 4. Outbound submission

Use the existing TLS-gated SMTP AUTH event for customer submission. Add
per-domain DKIM signing, authenticated identities, quotas, bounce webhooks, and
reputation monitoring. HTTP email submission can share the same durable mail
queue.

### 5. IMAP and POP3 retrieval gateways

Add TLS-enabled IMAP and POP3 listeners. Authenticate through the planner,
resolve a deployment-registered mailbox upstream, and proxy the session with
bounded connections, idle deadlines, cancellation, and audit events. Start
with native IMAP/POP3 upstreams rather than interpreting mailbox storage.

### 6. Storage adapters outside core

Publish the delivery-adapter and mailbox-upstream interfaces plus HTTP and
proxy examples. External systems may implement durable message acceptance and
retrieval. Search, encryption, retention, mailbox indexing, and CalDAV/CardDAV
remain their modules rather than Gateway storage responsibilities.

## First implementation slice

`src/spinner/cmd/bridge/smtpout` implements authenticated relay submission with plaintext,
STARTTLS, or implicit TLS transport modes. Plaintext is intended only for
local/private test relays; production configuration should use STARTTLS or
implicit TLS. Typed SMTP `GatewayDelivery` values now run through a durable
filesystem spool and the deployment-registered SMTP adapter. The proxy runtime
enables that adapter when `GATEWAY_SMTP_RELAY_ADDR` and
`GATEWAY_DELIVERY_SPOOL_PATH` are configured.

Enqueue returns only after the spool file and containing directory are synced.
Pending deliveries survive restart, retry with bounded exponential backoff,
and move to a separate failed spool after the configured attempt limit. Queue
events report queued, deferred, delivered, and failed states. The spool is
single-process and local-disk owned; sharing one spool path between Gateway
processes is unsupported. A future distributed adapter can implement the same
`GatewayDeliveryQueue` interface.
