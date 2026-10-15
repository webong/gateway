# Building a hosted mail provider on Gateway

Gateway supplies MX ingress, recipient policy, durable delivery, outbound SMTP
submission, and IMAP transport. The provider owns account and mailbox records,
message storage and indexing, user authentication, abuse controls, and its
public product API. Existing alias forwarding, MX hooks, and external mailbox
connections remain available for other addresses and accounts.

## Receive into a provider mailbox

1. Create and TXT-verify the domain through `/email/domains`, then enable it.
   Point its MX records at the Gateway SMTP listener.
2. Implement `Webong\Gateway\Email\EmailProviderRouteResolver` in the Planner
   application. `resolve($recipient)` returns `null` for addresses owned by
   Gateway aliases or hooks, or an `EmailProviderRoute($accountId,
   $adapterName, $mailboxKey)` for an active provider mailbox. The resolver must
   check tenant ownership and mailbox status against the provider's trusted
   records. Configure its class with `GATEWAY_EMAIL_PROVIDER_ROUTE_RESOLVER`.
3. Register the same namespaced adapter in Spinner. For an HTTPS acceptance
   endpoint, configure an operator-owned `GATEWAY_HOSTED_MAIL_ADAPTERS` JSON map:

   ```json
   {"zorvia.mailbox":{"url":"https://mail.internal.example/ingest","bearer_token":"replace-with-private-token"}}
   ```

   A Caddy SMTP deployment can use `hosted_mail_adapters` with the same map.
   Configure a durable spool or shared PostgreSQL queue. The provider endpoint
   and token are deployment configuration, never values supplied by a tenant
   route or a queued message.

The route is evaluated during recipient preflight and again for DATA. Gateway
requires a verified, enabled domain and one recipient per transaction. A
provider route has priority over a Gateway hook or alias at that address; the
resolver should return `null` when those other routes should win. A catch-all
hook handles otherwise unclaimed addresses only when no alias matches.

The stock HTTP adapter sends `POST` with the raw RFC 5322 message as
`message/rfc822`. It includes `Authorization: Bearer <operator token>`,
`X-Gateway-Event-Id`, `Idempotency-Key`, and `X-Gateway-Envelope`. The envelope
is base64 JSON with `account_id`, `mailbox_key`, `mail_from`, and `recipient`.
Treat the mailbox key as a logical identifier, not as a URL or filesystem path.
Authenticate the operator token, recheck that the mailbox belongs to the
account, then durably store the message and idempotency key in one transaction
before returning `2xx`. Repeated keys must return success without storing a
second message. `408`, `429`, `5xx`, and network failures are retried; ordinary
`4xx` is a permanent rejection. Gateway does not read provider response bodies.

For an in-process provider adapter, link a Go module that calls
`transport.RegisterDeliveryAdapterFactory("zorvia.mailbox", factory)` before
starting Spinner or Caddy. The factory returns a
`bridge.GatewayDeliveryAdapter`. The route's adapter name must be registered
on every worker that may claim its queue entries. Adapters are selected only
from operator registrations, not dynamically loaded from recipient data.

## Send and retrieve

Gateway's HTTPS submission and IMAP listeners can authenticate through
deployment-owned Planner authenticators and proxy to approved upstream SMTP
and IMAP services. The provider supplies mailbox credentials or implements
the appropriate authentication interfaces; Gateway does not create mailbox
storage. The separate external mailbox-connection path stores independent
IMAP and SMTP credentials, tests both, and resolves current SMTP credentials
at delivery time. See [mail forwarding](mail-forwarding.md) for its management
API and configuration.

Use the delivery receipt API to observe accepted, retried, and failed queue
entries. A `250` SMTP DATA response means the delivery manifest was durably
queued; it does not mean the provider stored the message. One parent manifest
can fan out to multiple hook subscribers, with a deterministic child receipt
per subscriber. HTTP delivery is at least once, so the provider must enforce
its idempotency key. An SMTP upstream can have uncertain acceptance after a
connection failure; Gateway stops automatic retries and marks that receipt
`smtp_acceptance_unknown` for reconciliation.

## Verification before production

The repository tests cover two isolated provider accounts, transient HTTP
failure and retry, route authorization, MX hook and alias coexistence, and
durable fan-out. An operator should additionally run a disposable deployment
with two actual domains/accounts: receive over MX, read via IMAP, submit via
HTTPS, verify each account's selected SMTP server, read a later reply, and
exercise disabled, revoked, and transient-failure cases. Authorized live tests
are needed before claiming compatibility with Google, Microsoft, or any other
real upstream. DNS, deliverability, reputation, storage retention, and abuse
operations belong to the hosted provider's deployment plan.
