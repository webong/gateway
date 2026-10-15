# Gateway mail forwarding plan

Gateway is a mail transport and routing plane. It accepts SMTP transactions and
asks the Laravel planner whether to accept, reject, or deliver them. A hosted
mail provider can build on these rails to offer a complete service comparable
to Forward Email; Gateway itself does not need to host mailboxes, operate a
webmail product, or own customers' sending reputation. Its contracts must let
that provider implement those capabilities without changing the core SMTP,
IMAP, HTTP submission, webhook, or durable delivery paths.

Gateway's mail surface has three complementary routes: MX-to-SMTP alias
forwarding, MX-to-HTTP webhook hooks, and client access to an external IMAP and
SMTP provider. A hosted provider can register its own durable delivery adapter
and mailbox upstream behind the same interfaces. Product parity is evaluated
on whether that provider can be built and operated on Gateway, not on whether
Gateway ships every hosted-service feature itself.

## MX mail hooks

Mail hooks are an opt-in MX receiving path alongside alias-to-SMTP forwarding
and external IMAP mailbox connections. They require `GATEWAY_EMAIL_HOOKS_ENABLED=true`,
an enabled TXT-verified domain under `/email/domains`, a durable delivery spool
or shared PostgreSQL queue, and recipient preflight. `POST /email/hooks` with
`{"domain":"example.com","local_part":"support"}` and the registry bearer
token creates a managed endpoint for that address. Omitting `local_part`
creates a catch-all hook for addresses without a matching alias or exact hook.
The response includes its `endpoint_key`
and `subscriptions_url`. Add `relay` subscriptions to that endpoint with
`routing_scope: smtp`, `routing_key: recipient`, and a trusted HTTPS URL.
`GET/PATCH/DELETE /email/hooks/{id}` lists status, toggles `active`, or disables
the hook. Re-enabling rechecks domain ownership.

For an active hook, Gateway checks the domain before accepting the recipient,
routes one recipient per SMTP transaction, and queues the raw RFC 5322 MIME
body in one durable fan-out manifest before replying `250` to DATA. A worker
then creates one deterministic receipt per subscriber. The webhook
body is `message/rfc822`. `X-Gateway-Event-Id` carries the transaction ID;
`X-Gateway-Envelope` is base64-encoded JSON with `mail_from` and `recipients`.
The durable HTTP adapter owns retry and failed-delivery receipts. An exact hook
takes precedence over an alias at the same address; an alias takes precedence
over a catch-all hook, including when the alias is paused. A hook with no subscribers
rejects the message. MX DNS still has to point at this Gateway deployment;
domain verification alone does not change the customer's MX records.

If expansion stops after some child receipts are written, the parent retries
using the same child IDs and fingerprints. Each child has its own delivery
receipt and retry outcome. Webhook delivery remains at least once: a worker
can crash after a subscriber accepts an HTTP request but before recording its
receipt. Subscribers should deduplicate using Gateway event and delivery
identifiers. The existing forwarding path itself remains an early capability with
separate spam, abuse, and reputation gaps described below. Mail hooks do not
provide mailbox storage, IMAP synchronization, or customer-specific SMTP
reply sending.

## Customer mailbox connections

Mailbox connections are a separate opt-in path for customers whose mail lives
on an external IMAP/SMTP provider. Run the new `mailbox_accounts` and
`mailbox_connections` migration. Configure the same approved upstream IDs in
Planner (`GATEWAY_EMAIL_IMAP_UPSTREAMS`, `GATEWAY_EMAIL_SMTP_UPSTREAMS`) and
Spinner (`GATEWAY_IMAP_UPSTREAMS`, `GATEWAY_MAILBOX_SMTP_UPSTREAMS`). Each JSON
map entry names an operator-controlled `address` and `server_name`; SMTP also
requires `tls_mode` (`implicit` or `starttls`). Set
`GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR` to
`Webong\Gateway\Email\ConnectedIMAPAuthenticator` and
`GATEWAY_EMAIL_SUBMISSION_AUTHENTICATOR` to
`Webong\Gateway\Email\ConnectedSMTPAuthenticator`. Enable existing IMAP and
HTTPS submission listeners, set `GATEWAY_EMAIL_CONNECTED_SMTP_ENABLED=true`,
and give Spinner durable delivery storage, `GATEWAY_INTERNAL_TOKEN`,
`GATEWAY_MAILBOX_SMTP_RESOLVER_URL` pointing to Planner's private origin, and
a private delivery admin listener. The Caddy SMTP app supports equivalent
`mailbox_resolver_url` and `mailbox_upstreams` settings.

An operator creates an account with `POST /email/mailbox-accounts` using the
registry token and `{"id":"customer-id"}`. The returned account bearer token
manages only `/email/mailbox-accounts/{account}/connections`. Create with an
email `address`, separate `imap_upstream_id` and `smtp_upstream_id`, separate
usernames and secrets, and each side's `auth` (`password`, `google`, or
`microsoft`). The create response returns one-time IMAP and SMTP client
passwords. Clients use the connection UUID as their username. `POST .../test`
probes both upstream logins without submitting mail; `PATCH` with
`{"enabled":true}` requires a healthy probe. `PUT` replaces all upstream
settings, disables the connection, and requires another test. `DELETE` removes
it. Management requires HTTPS. Secrets are encrypted at rest and omitted from
read responses. Each queued SMTP message stores only the connection UUID;
the worker fetches current credentials and approved routing at delivery time.

For OAuth, set `GATEWAY_EMAIL_OAUTH_REFRESHER` to
`Webong\Gateway\Email\ProviderMailboxOAuthRefresher` and configure the matching
`GATEWAY_EMAIL_GOOGLE_CLIENT_ID`/`CLIENT_SECRET` or
`GATEWAY_EMAIL_MICROSOFT_CLIENT_ID`/`CLIENT_SECRET` and optional
`GATEWAY_EMAIL_MICROSOFT_TENANT`. This refreshes Google or Microsoft tokens at
their fixed token endpoints, using transport-specific Microsoft IMAP or SMTP
scopes, and stores a rotated refresh token when returned. A deployment may
instead supply a `MailboxOAuthRefresher` implementation. The queue never stores
access tokens. OAuth authorization and initial consent must supply a refresh
token with the provider's IMAP or SMTP permission. Google and Microsoft real-provider
compatibility still needs authorized live tests, including token rotation,
revocation, and provider-specific permissions. Connection testing is a
credential probe, not a guarantee that a provider will accept later DATA.

## Product boundary

- Go owns SMTP sockets, TLS, message limits, outbound submission, timeouts, and
  the durable delivery queue.
- Laravel owns domains, ownership verification, aliases, catch-all and pattern
  matching, tenant limits, credentials, and delivery policy.
- Raw RFC 5322 messages cross the existing protocol event contract unchanged.
- On relay-backed forwarding only, Spinner adds a signed
  `X-Gateway-Forwarded` trace header to the outbound copy. The original
  Planner event and SMTP-to-HTTP deliveries remain unchanged.
- Gateway does not own mailbox storage or indexing. It may expose IMAP and
  POP3 transport gateways backed by an external mailbox system.

The outbound mail module exposes one operation: submit an envelope and message
through a configured SMTP relay. The implementation hides connection setup,
TLS, authentication, and transaction lifecycle from callers.

If an SMTP DATA write or final acknowledgement fails without a definitive
server rejection, the sender reports uncertain acceptance. Both durable queue
backends stop automatic retries, retain a failed receipt marked
`body_assertion: smtp_acceptance_unknown`, and suppress an automatic failure
notice. An operator must reconcile this result with the upstream before any
manual resend. A process crash or lease loss after upstream acceptance and
before the receipt is durably settled can still cause a duplicate; Gateway
does not promise exactly-once SMTP delivery.

## Extension seam

Every accepted message is an immutable envelope plus its raw RFC 5322 payload.
The durable delivery queue dispatches that value to registered delivery
adapters. SMTP and HTTP forwarding are built-in durable adapters when a
delivery spool is configured. Standalone HTTP forwarding retains the
in-process worker path without `GATEWAY_DELIVERY_SPOOL_PATH`.
The Caddy SMTP app also spools HTTP subscriber deliveries when `spool_path`
is set, even without an outbound SMTP relay.
`GatewayDelivery.protocol` describes the message protocol, while the optional
`GatewayDelivery.adapter` selects a trusted implementation. When omitted, the
adapter defaults to the protocol name. Namespaced adapters such as
`vendor.mailbox` can therefore accept SMTP messages without pretending to be
an SMTP server.

A storage vendor or application can add another adapter without changing SMTP
ingress, alias resolution, retry scheduling, or the message contract. The
adapter owns storage-specific encryption, indexing, retention, and quotas.
Gateway records whether the adapter accepted the delivery and exposes the
resulting receipt. A linked provider module registers its named adapter with
`transport.RegisterDeliveryAdapterFactory` before the Spinner or Caddy runtime
starts. The stock runtime also accepts operator-approved HTTPS provider
profiles in `GATEWAY_HOSTED_MAIL_ADAPTERS` or Caddy's `hosted_mail_adapters`.
The Planner selects one profile through a deployment-owned
`EmailProviderRouteResolver`. See [Hosted provider contract](hosted-mail-provider.md).

Retrieval uses a separate upstream seam. The POP3 and IMAP listeners own public
TLS, pre-auth command parsing, session limits, and client lifecycle. After the planner
authenticates and resolves the account, the listener connects to a trusted
mailbox upstream. The upstream owns mailbox state, flags, UIDs, folders,
search, retention, and message bodies.

The first retrieval adapters are transparent POP3 and IMAP upstream proxies.
This lets Gateway provide custom-domain retrieval without reimplementing
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

The first Planner-owned control-plane slice now stores `email_domains` and
`email_aliases`. The `REGISTRY_TOKEN`-authenticated `/email/domains` API creates
a disabled domain and returns a TXT challenge at
`_gateway-verify.<domain>`. `POST /email/domains/{id}/verify` checks that
record on every call; enabling a disabled domain rechecks live TXT ownership
before accepting mail. A missing record or unavailable DNS lookup leaves the
domain disabled. For an enabled domain, Planner rechecks TXT ownership on the
next SMTP recipient or DATA decision once `verified_at` is older than
`GATEWAY_EMAIL_VERIFICATION_TTL_SECONDS` (default 86400 seconds; `0` checks
every decision). Until a successful recheck, missing TXT or a DNS lookup
failure produces a temporary SMTP `451`, so the sender can retry. This is
traffic-triggered revalidation, not a background DNS polling service.
Exact local-part aliases, prefix patterns such as `team*`, and one catch-all
alias (`local_part: "*"`) per domain
can each hold 1–10 recipients and be listed, updated, paused, or deleted under
`/email/domains/{id}/aliases`. Only a final `*` is permitted in a pattern;
arbitrary regular expressions are not executed. A verified, enabled domain
resolves an exact alias first, then the longest matching prefix, then the
catch-all. A paused best match rejects its address rather than falling through
to a broader pattern or catch-all. Patterns and catch-all may attract unwanted
mail and should be enabled only with deliberate abuse
controls. All managed recipients in one message
are combined into one `submit` decision and one outbound SMTP relay
transaction; an unknown, disabled, or mixed managed/unmanaged recipient set
rejects the whole inbound transaction. Spinner returns success only after the
configured relay accepts the full message. This avoids a Gateway-side deferred
bounce obligation for this path, but does not guarantee final mailbox delivery.
Configure `GATEWAY_SMTP_RELAY_ADDR` and `GATEWAY_DELIVERY_SPOOL_PATH` on
standalone Spinner, or `relay_address` and `spool_path` in the Caddy SMTP app.
When an outbound relay is configured, recipient preflight is enabled by
default in standalone Spinner and the Caddy SMTP app. Spinner asks Planner for
an `smtp`/`recipient` decision at each `RCPT TO`, including the recipients
already accepted for that transaction as a JSON array in
`attributes.accepted_recipients`. Unknown, disabled, or mixed managed and
unmanaged recipients receive a `550` before DATA; Planner failures receive a
temporary `451`. A custom planner that does not support recipient events may
explicitly set `GATEWAY_SMTP_RECIPIENT_PREFLIGHT=false` or
`recipient_preflight false`; with no outbound relay, preflight remains off by
default. A custom planner used with preflight enabled must return `accept` or
`reject`/`close` for recipient events. DATA is still
planned and checked independently because alias state may change between the
commands.
Without a relay, the submit path returns a temporary SMTP error.
`make test-email` exercises domain creation and verification, alias setup,
multi-recipient forwarding, and exact SMTP rejection codes against a local
Planner, Spinner, and test relay. The TXT resolver used by this smoke is
test-only; production verification performs a real DNS TXT lookup.

### 3. Forwarding correctness

Add spam decisions, ARC sealing, bounce and reputation monitoring, and broader
abuse controls. Do not advertise reliable internet forwarding
before these protections are deployed. In particular, current managed Email
forwarding is an operator-enabled early capability, not a production-grade
public mail-forwarding service: preflight can be disabled, and most abuse
controls remain absent. Spinner evaluates SPF and DKIM; Planner assesses
DMARC and can optionally quarantine conclusive `p=quarantine` or `p=reject`
failures. DMARC aggregate and failure reporting remain absent. An opt-in,
two-way opaque SRS return token now rewrites the outbound envelope and reverse-routes validated
null-sender bounces; see the [Email authentication gate](email-authentication.md)
for configuration, key lifetime, and public MX/SPF requirements.
For managed Email, a deployment may now register a trusted
`Webong\Gateway\Email\EmailMessagePolicy` class with `GATEWAY_EMAIL_POLICY`.
Planner calls it after recipient resolution and DMARC assessment, but before
issuing an SRS return address or handing the message to the outbound relay.
It receives the complete original `GatewayEvent`, resolved destination list,
and optional `DmarcAssessment`, and returns `EmailPolicyVerdict::Accept`,
`Reject`, or `Defer`. A rejection yields SMTP `550`; a deferral yields `451`.
The response is deliberately generic so scanner details are not exposed to
the SMTP client. Only deployment-owned code may implement this interface;
clients and domains cannot select a policy class. A scanner must bound its
own network calls within the Planner deadline. Configuring this hook alone
does not satisfy the public-forwarding gate.

Gateway has an operator-managed Email suppression list shared by Planner
replicas. Apply the `email_suppressions` migration, then use the registry bearer
token to add a canonical address and a reason (`manual`, `hard_bounce`, or
`complaint`) with `POST /email/suppressions`. `GET /email/suppressions` is
cursor-paginated (`limit` 1–100, `cursor` from `next_cursor`); remove an entry
with `DELETE /email/suppressions/{id}`. A new authenticated SMTP transaction,
new HTTP submission, or managed alias forwarding transaction is rejected if
any outbound recipient is suppressed. HTTP idempotency retries for a previously
accepted receipt still return that receipt, and a suppressed new submission
does not consume quota. The API is operator-controlled; Gateway does not yet
ingest provider hard-bounce or complaint events automatically. Suppression is
checked at acceptance, not again at queue dispatch, so mail already queued
before an address is added may still be delivered. Do not treat this as a
complete reputation or abuse-control system.

For example, a host application can implement the interface in
`App\Gateway\MailPolicy`, register its dependencies with Laravel, and set
`GATEWAY_EMAIL_POLICY='App\Gateway\MailPolicy'` on every Planner worker.
Return `Defer` when a scanner cannot complete; do not treat a scanner outage
as `Accept`. The policy receives raw message bytes, so its logs and external
scanner requests need the same privacy controls as the SMTP transport.

Spinner also has an optional built-in ClamAV `clamd` malware check for resolved
SMTP submit/deliver decisions. It runs after Planner accepts the transaction
and before forwarding or delivery. Configure a private Unix socket (preferred)
or private TCP connection:

```text
GATEWAY_EMAIL_CLAMD_NETWORK=unix
GATEWAY_EMAIL_CLAMD_ADDR=/run/clamav/clamd.sock
GATEWAY_EMAIL_CLAMD_TIMEOUT=10s
GATEWAY_EMAIL_QUARANTINE_PATH=/var/lib/gateway/email-quarantine
GATEWAY_EMAIL_QUARANTINE_RETENTION=720h
```

The quarantine path is optional and can be used for Planner-directed DMARC
quarantine without a malware scanner. A clean scanner result continues to
delivery. A scanner error or storage error defers the message
with SMTP `451`; an infected message is rejected with `554` if quarantine is
not configured. When a scanner flags infection or Planner orders quarantine,
Spinner durably stores the original message and acknowledges it without
forwarding. The path must be an absolute,
private directory on a persistent volume. Restrict backups and access: it
contains raw messages and envelope metadata. If storage sync fails after a
rename, a sender retry can create a second copy; operators should use the
message digest when reconciling duplicates. Set clamd's `StreamMaxLength` at
least as high as `GATEWAY_SMTP_MAX_MESSAGE_SIZE`. Do not expose clamd TCP on a
public network: its protocol has no authentication or encryption.
Retention is opt-in (`0` disables automatic deletion). With a positive Go
duration, Spinner deletes older records before it starts the SMTP listener and
then checks hourly. Startup fails if the first prune cannot safely inspect the
store; later prune errors are logged. Deletion is permanent, so choose a
retention window and backup policy that match your obligations.

Local operators can inspect and export an item without a public API:

```sh
go run -mod=mod ./src/spinner/cmd/email-quarantine list -path /var/lib/gateway/email-quarantine
go run -mod=mod ./src/spinner/cmd/email-quarantine show -path /var/lib/gateway/email-quarantine -id ID
go run -mod=mod ./src/spinner/cmd/email-quarantine export -path /var/lib/gateway/email-quarantine -id ID -output /absolute/review.eml
go run -mod=mod ./src/spinner/cmd/email-quarantine delete -path /var/lib/gateway/email-quarantine -id ID
```

There is no automatic release, spam verdict, or tenant-facing quarantine.
The trusted PHP policy hook remains separate and may reject or defer before
Spinner's built-in scanner runs. Configure and
test operational retention before accepting public traffic.

For a local real-scanner check, start an
[official ClamAV Debian image](https://docs.clamav.net/manual/Installing/Docker.html)
on a loopback-only port, wait for `docker inspect` to report `healthy`, and
then run:

```sh
docker run --rm -d --name gateway-clamd-test -p 127.0.0.1:13310:3310 clamav/clamav-debian:stable
docker inspect --format '{{.State.Health.Status}}' gateway-clamd-test
GATEWAY_TEST_CLAMD_ADDR=127.0.0.1:13310 go test -mod=mod ./src/spinner/cmd/bridge/smtp -run 'TestClamdScannerAgainstRealService|TestSMTPRealClamdQuarantine' -count=1 -v
docker stop gateway-clamd-test
```

The opt-in tests send clean data and the harmless
[EICAR test string](https://www.eicar.org/download-anti-malware-testfile/),
including one complete SMTP transaction that must be quarantined without
reaching delivery. These checks do not prove a production Planner, relay,
retention policy, or public MX deployment.

SMTP connections are capped per Spinner
process with `GATEWAY_SMTP_MAX_CONNECTIONS` (or `max_connections` in Caddy).
`MAIL` commands are limited per connecting IP by
`GATEWAY_SMTP_MESSAGES_PER_MINUTE` and `GATEWAY_SMTP_MESSAGE_BURST` (or
`messages_per_minute` and `message_burst` in Caddy). This in-memory limit is
per process, not per domain or authenticated identity; a shared TCP proxy
makes its address the apparent source. It is not a replacement for mail abuse
policy or network-level controls. Spinner now signs each outbound forwarding
hop using `GATEWAY_INTERNAL_TOKEN`; a returned message with three valid
Gateway hop markers, or at least 100 `Received` headers, is rejected before
another relay attempt. Unsigned headers do not count. This guard only works
while intermediaries preserve the trace header and deployments share the same
internal token; it is not a general internet-loop guarantee. A lost inbound
`250` after the upstream relay accepted DATA can cause a duplicate retry.
Do not treat local SRS bounce tests as proof of public forwarding correctness.
The [Email authentication gate](email-authentication.md) records the remaining
deployment and abuse-control acceptance tests.

### 4. Outbound submission

SMTP AUTH is TLS-gated and sends credentials only to the private Planner. The
built-in registry planner rejects AUTH rather than forwarding a password to a
subscription callback. Configure `GATEWAY_EMAIL_SUBMISSION_AUTHENTICATOR` with
a deployment-owned class implementing `EmailSubmissionAuthenticator`; its
`authorize(GatewayEvent $event)` method must verify the credential and return
an `EmailSubmissionGrant` containing a stable, canonical account identity and
the exact envelope sender addresses granted to that login, or `null` to deny
it. A custom `ProtocolPlanner` may instead handle AUTH directly, but must
return an `accept` decision with `metadata.auth_identity` set to that canonical
identity and `metadata.authorized_mail_from` as a JSON array of exact
addresses (for example, `["sender@example.com"]`). Spinner rejects an AUTH
decision without a non-empty valid grant, and rejects `MAIL FROM` outside the
grant with SMTP 553. The grant is connection-local and cleared on logout;
wildcards, null senders, and display-name forms are not accepted. This
authorizes the envelope and requires the visible RFC 5322 `From` address to be
one of the same granted addresses. Never accept AUTH through a generic
registry route.

Per-domain outbound DKIM signing is opt-in. Set
`GATEWAY_EMAIL_DKIM_SIGNING_CONFIG=/etc/gateway/dkim.json` on Spinner (or
`dkim_signing_config` in the Caddy SMTP app) and mount an operator-owned JSON
file containing one key per author domain:

```json
[{"domain":"example.com","selector":"mail1","key_file":"/etc/gateway/keys/example.com.pem"}]
```

The private key must be a single PKCS#8 Ed25519 or PKCS#8/PKCS#1 RSA PEM file
with owner-only permissions; RSA keys must be at least 2048 bits. Publish the
corresponding TXT public key at `mail1._domainkey.example.com` before sending.
Spinner, not Planner or the SMTP client, marks the authorized RFC 5322 author
domain. The outbound adapter signs the final message bytes immediately before
relay submission, including on durable-queue retries. If signing is configured
but that author domain has no key, authenticated SMTP submission is deferred
before accepting DATA rather than queueing mail that cannot be signed. The
adapter also refuses such a delivery if it reaches the queue by another path.
With no signing configuration, existing submission remains
unsigned. Rotate selectors by publishing the new DNS key before switching the
configuration and restarting every Spinner/Router instance. Retain the old
public key until previously sent mail has cleared.

For a shared per-account daily submission-attempt quota, set
`GATEWAY_EMAIL_SUBMISSION_DAILY_LIMIT` on every Planner worker. `0` (the
default) disables the global limit. An authenticator may return an
`EmailSubmissionGrant` with a positive `dailyLimit` to override it for one
account. Spinner carries the canonical `auth_identity` and approved limit
from AUTH to each SMTP transaction; the username spelling supplied by a client
is never the quota key. Planner atomically increments one hashed-identity row
in its shared database per UTC day and replies with SMTP 452 once the limit is
reached. Set `GATEWAY_EMAIL_SUBMISSION_DAILY_BYTE_LIMIT` (default `0`, disabled)
for an independent per-account cap on raw SMTP DATA bytes, or let the
authenticator return `dailyByteLimit` for an account-specific override (up to
1 TB). The same atomic update charges both attempt and byte usage before route
planning. A message rejected by either quota is charged to neither; a message
admitted by the quotas counts even if later policy or relay delivery fails.
Set `GATEWAY_EMAIL_SUBMISSION_DOMAIN_DAILY_LIMIT` and/or
`GATEWAY_EMAIL_SUBMISSION_DOMAIN_DAILY_BYTE_LIMIT` for independent UTC-day
caps shared by **all authenticated accounts** sending from the same
case-insensitive envelope-sender domain. Each cap defaults to `0` (disabled).
Planner updates account and domain counters in one transaction: reaching
either cap rejects the message without charging the other counter. Domain
caps apply to SMTP AUTH and HTTP submission alike, but not to unauthenticated
inbound mail. All Planner workers must use the same database and quota
configuration; install the domain-usage migration before enabling the caps.
These are abuse-control limits, not counts of delivered mail. Run
`php artisan gateway:prune-email-submission-usage` periodically to remove
rows older than `GATEWAY_EMAIL_SUBMISSION_USAGE_RETENTION_DAYS` (default 90).
The limits require the Planner usage, byte-column, and domain-usage migrations
and a shared database across workers;
a database failure defers submission rather than bypassing the quota.

Set `GATEWAY_EMAIL_HTTP_SUBMISSION_ENABLED=true` on Planner to opt in to HTTP
email submission at `POST /email/submissions`. This public route requires HTTPS
and HTTP Basic credentials checked by the
same deployment-owned `EmailSubmissionAuthenticator` as SMTP AUTH; it does not
accept `REGISTRY_TOKEN` as a sender credential. Supply a JSON body with
`mail_from`, a list of 1–100 `recipients`, and `message_base64` containing the
complete RFC 5322 message. The envelope sender and the single visible `From`
address must match one exact address in the authenticator's grant. The
account and sender-domain daily attempt and byte limits apply to HTTP and SMTP
together.
The raw message limit is 10 MiB; configure the public proxy and Laravel's
request-size settings to admit the base64 JSON envelope (up to about 14 MiB).

```sh
curl --user 'account:password' \
  -H 'Idempotency-Key: invoice-2026-10-02-1' \
  -H 'Content-Type: application/json' \
  -d '{"mail_from":"sender@example.com","recipients":["recipient@example.net"],"message_base64":"RnJvbTogc2VuZGVyQGV4YW1wbGUuY29tDQoNCkhlbGxv"}' \
  https://gateway.example.com/email/submissions
```

Planner calls `POST /email/submissions` on one private Spinner delivery-admin
node using `GATEWAY_INTERNAL_TOKEN`. Configure `GATEWAY_DELIVERY_ADMIN_ADDR`
on Spinner and `GATEWAY_DELIVERY_ADMIN_URL` or `GATEWAY_DELIVERY_ADMIN_NODES`
on Planner, together with the outbound SMTP relay and a persistent delivery
spool. Do not publish the admin listener. When behind a TLS terminator,
configure Laravel's trusted proxies so it recognizes the original HTTPS
scheme; plain HTTP requests are rejected before credentials are processed.
Spinner repeats exact sender/author checks, signs the loop marker, checks an
available DKIM key, runs the optional clamd scanner, and returns a durable
receipt ID only after the spool write succeeds. A `202` response means queued,
not accepted by the outbound relay; inspect `/deliveries/{id}` with the
registry management token for later status. Queue and scanner failures return
`503`; malformed or unauthorized messages do not reach the queue. Every HTTP
submission requires an `Idempotency-Key` of 1–128 letters, digits, dots,
underscores, colons, or hyphens. Planner stores only hashes of the canonical
account, key, and message, plus a reserved receipt ID and owning node, in the
shared database. The first request charges the shared quota before queue
handoff; retries with the same account, key, and exact envelope/message reuse
the receipt without another quota charge. Changing the request under the same
key returns `409`. If the first private response is lost, Planner retries the
same receipt on the same Spinner node and Spinner deduplicates it from its
durable spool, even after a process restart. The guarantee lasts 24 hours;
`gateway:prune-email-submission-usage` removes expired keys. Keep the owning
node and its spool available through that window. If the node or spool is lost,
the request fails rather than silently moving to another node. This protects
HTTP enqueue from duplicate retries; it does **not** make the outbound SMTP
relay exactly-once after an ambiguous relay response. A rejected or unavailable
first queue handoff still consumes one attempt/byte quota allocation for the
key, matching SMTP submission accounting.

When Spinner has `GATEWAY_EMAIL_DSN_DOMAIN` configured, an authenticated HTTP
submission also opts into Gateway's existing durable failure-notice path. If
its queued relay delivery finally fails, Spinner sends one RFC 3464 notice
with a null reverse-path to the exact envelope sender that Planner granted
and Spinner rechecked. This is a local relay-failure notice, not a webhook and
not a report of a later bounce after the relay accepted the message. Without
the DSN domain, HTTP submission does not generate a notice; operators can
still inspect its delivery receipt.

Bounce webhooks and reputation monitoring are still required for a full
submission service.

### 5. IMAP and POP3 retrieval gateways

The initial POP3 and IMAP paths are implemented in Spinner for standalone
HTTP/RoadRunner and bundled Router deployments. Both accept implicit TLS only
(normally ports 995 and 993), authenticate through a deployment-owned Planner
`MailboxAuthenticator`, then proxy the authenticated session to a native
upstream over verified implicit TLS. POP3 accepts `USER`/`PASS`; IMAP accepts
atom, quoted, or synchronizing-literal `LOGIN` arguments (each literal at most
2,048 bytes) and SASL `AUTHENTICATE PLAIN` (with or without an initial response).
After login, mailbox commands and responses are passed through
without interpreting message content. Planner returns a registered upstream ID
and mapped credentials, never an arbitrary network address. Spinner limits
connections and applies idle, Planner, and upstream deadlines. It sends
`pop3` or `imap` connect events before tunneling and best-effort close events
on exit; these callbacks are not a durable session audit log. The default
Planner authenticator is absent, so mailbox login fails closed.
Spinner also bounds password attempts per socket peer across connections before
calling Planner. Each protocol defaults to 30 attempts/minute with a burst of
5, local to each Spinner process; this is not a distributed account-lockout
policy.

Set `GATEWAY_POP3_ADDR=:995`, `GATEWAY_POP3_HOSTNAME`,
`GATEWAY_POP3_TLS_CERT_FILE`, `GATEWAY_POP3_TLS_KEY_FILE`, and
`GATEWAY_POP3_UPSTREAMS` (a JSON object keyed by stable IDs, for example
`{"primary":{"address":"pop.internal:995","server_name":"pop.internal"}}`).
The address must point to an operator-trusted POP3 server with a certificate
valid for `server_name`; Spinner verifies it against the host trust store.
Planner sets `GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR` to a container-resolvable
class implementing `Webong\Gateway\Email\MailboxAuthenticator`, returning a
`MailboxAccessGrant` after validating client credentials. The private
Spinner-to-Planner connection carries the client password; protect it with
Unix socket or private TLS and never expose the internal event endpoint.
Optional limits are `GATEWAY_POP3_MAX_CONNECTIONS`,
`GATEWAY_POP3_AUTH_PER_MINUTE`, `GATEWAY_POP3_AUTH_BURST`,
`GATEWAY_POP3_IDLE_TIMEOUT` (minimum ten minutes),
`GATEWAY_POP3_PLANNER_TIMEOUT`, and `GATEWAY_POP3_UPSTREAM_TIMEOUT`.

For IMAP set `GATEWAY_IMAP_ADDR=:993`, `GATEWAY_IMAP_HOSTNAME`,
`GATEWAY_IMAP_TLS_CERT_FILE`, `GATEWAY_IMAP_TLS_KEY_FILE`, and
`GATEWAY_IMAP_UPSTREAMS` using the same ID-to-`address`/`server_name` JSON
shape as POP3, but pointing to native implicit-TLS IMAP upstreams. Optional
limits are `GATEWAY_IMAP_MAX_CONNECTIONS`, `GATEWAY_IMAP_AUTH_PER_MINUTE`,
`GATEWAY_IMAP_AUTH_BURST`, `GATEWAY_IMAP_IDLE_TIMEOUT` (minimum ten minutes),
`GATEWAY_IMAP_PLANNER_TIMEOUT`, and `GATEWAY_IMAP_UPSTREAM_TIMEOUT`.

The custom FrankenPHP/Caddy build also registers `gateway.pop3` and
`gateway.imap` sibling TCP apps. They use the same TLS-only POP3/IMAP servers
and private HTTP Planner bridge as standalone Spinner. Configure either app
in the Caddy global block with `listen`, `planner_url`, `internal_token`,
`tls_cert_file`, `tls_key_file`, and one or more
`upstream <id> <host:port> <TLS server name>` entries. For example:

```caddyfile
{
    gateway_pop3 {
        listen :995
        planner_url http://127.0.0.1:8081
        internal_token {$GATEWAY_INTERNAL_TOKEN}
        tls_cert_file /etc/gateway/mail.crt
        tls_key_file /etc/gateway/mail.key
        upstream primary pop.internal:995 pop.internal
    }
    gateway_imap {
        listen :993
        planner_url http://127.0.0.1:8081
        internal_token {$GATEWAY_INTERNAL_TOKEN}
        tls_cert_file /etc/gateway/mail.crt
        tls_key_file /etc/gateway/mail.key
        upstream primary imap.internal:993 imap.internal
    }
}
```

The `planner_url` must be a private PHP-only listener as in the
[FrankenPHP example](../infra/caddy/Caddyfile.frankenphp.example); never expose
it publicly. Optional Caddyfile controls are `hostname`, `max_connections`,
`auth_per_minute`, `auth_burst`, `idle_timeout`, `planner_timeout`,
`upstream_timeout`, `max_body_size`, and `upstream_ca_file` for a private CA
certificate. Without that option the host trust store is used. The upstream
list is operator-owned;
Planner returns only an ID from that list, and the TLS certificate of the
chosen upstream is verified against its configured server name.

For an OAuth mailbox, `MailboxAccessGrant` may set
`upstreamAuthMechanism: 'XOAUTH2'` and provide a current access token in
`upstreamPassword`. Spinner sends the XOAUTH2 initial response to the
registered TLS upstream. The deployment's credential authority owns token
refresh and must supply a fresh token at each authentication; Gateway does not
store refresh tokens. The customer-facing IMAP login remains PLAIN/LOGIN over
TLS. Outbound `smtpout.Sender` also accepts an in-memory `OAuthToken` with a
username and uses SMTP XOAUTH2. The existing HTTP submission path still uses
the configured relay and does not select a customer SMTP connection. Never
persist an access token or password in delivery attributes: the durable queue
stores those attributes on disk. A connection-scoped credential authority and
delivery-time transport lookup are required before enabling customer-specific
SMTP routing. OAuth provider interoperability and refresh are not verified.

IMAP non-synchronizing login literals, client authentication mechanisms beyond PLAIN/LOGIN,
OAuth/other POP3 authentication methods, protocol-aware mailbox command
policy, persistent mailbox-session audit, and certificate reload are not
implemented. Retrieval content passes through the byte
tunnel and is not persisted by Gateway. The opt-in external-mailbox smoke
exercises standalone and Caddy app paths against separate official Dovecot
containers, including the Caddy private Planner hop and verified upstream TLS.
It does not establish interoperability with every mailbox server or mail client.

Run `./scripts/mailbox-smoke.sh` from the repository root with Docker and Go
available. It starts disposable IMAP and POP3 Dovecot containers on random
loopback ports, verifies both upstream certificates, runs the external-service
and Caddy app integration tests, and stops the containers on success or failure. The test
uses a fixed throwaway password and stores no user mail. Override
`GATEWAY_MAILBOX_SMOKE_IMAGE` to test a pinned Dovecot image version.

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
SMTP DATA accepts a `deliver` decision only when its executor reports a
durable queue; a volatile in-process worker handoff returns a temporary SMTP
error. This prevents a crash after `250` from losing a newly accepted message,
and a custom Planner may opt an authenticated SMTP transaction into failure
notices for deliveries that fail later. Set `GATEWAY_EMAIL_DSN_DOMAIN` to a
Gateway-owned reporting hostname with an outbound SMTP relay and durable spool.
The custom Planner must first verify that the authenticated identity in
`event.attributes.auth_username` may use the transaction's `MAIL FROM`, then
return `metadata.failure_notice_to` equal
to that exact envelope sender on a `deliver` decision containing SMTP
deliveries. Spinner rejects a mismatched or unauthenticated request before
acknowledging DATA. It strips any notice marker from delivery attributes and
adds its own only after those checks. Managed Email uses synchronous `submit`
instead and does not generate this Gateway-side notice.

After final queue failure, Spinner durably enqueues one
[RFC 3464](https://www.rfc-editor.org/rfc/rfc3464.html) `multipart/report`
failure notice per SMTP delivery, addressed to the approved
reverse-path with `MAIL FROM:<>`. It does not copy the original message body
or raw relay error into the notice. A deterministic notice spool ID recovers
the handoff after a crash; `failure_notice_id` appears on the original failed
delivery status. Once a notice is queued, manual retry of the original failed
item is refused because a `failed` notice may already have been delivered.
The notice itself never generates another notice. If notice delivery fails,
its own failed spool item remains for operator inspection. As with other
queued SMTP deliveries, a lost upstream acceptance response can still lead
to a duplicate notice. Provide a monitored `postmaster@<dsn_domain>` address.
Do not enable this on unauthenticated public MX mail:
SMTP AUTH alone does not prove that its user owns the asserted reverse-path.
Verify the real relay and return address before using it in production.
Pending deliveries survive restart, retry with bounded exponential backoff,
and move to a separate failed spool after the configured attempt limit. Queue
events report queued, deferred, delivered, and failed states. The spool is
single-process and local-disk owned; sharing one spool path between Gateway
processes is unsupported. A future distributed adapter can implement the same
`GatewayDeliveryQueue` interface.
