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

## Application inboxes: primary receiving flow

For an application such as Zorvia, the primary path is account-owned SMTP
receiving followed by a signed HTTPS push of the unchanged RFC 5322 message.
IMAP connections remain optional for customers whose mailbox stays at another
provider. None of these paths gives Gateway mailbox storage.

An operator creates a generic account through HTTPS `POST /accounts` (or the
Mail alias `POST /email/mailbox-accounts`) with the registry bearer token. The
returned account token manages resources under
`/email/mailbox-accounts/{account}`; it cannot address another account.
The bundled Router image serves HTTPS on container port 8443 and optional
external-mailbox IMAP over TLS on 993, preserving the prior single-service
ports. Its plain HTTP listener on 8080 is loopback-only for health and private
use. It also starts an SMTP listener at container port 2525; publish host port
25 to 2525 when testing MX ingress. It uses the Router's durable delivery queue
on the persisted `/data` volume, requires recipient preflight, and resolves
sending credentials through the private loopback Planner listener. A PostgreSQL
deployment uses the shared delivery
database instead. This is a local transport setup, not evidence of public MX
readiness; production also needs TLS, abuse controls, public DNS and reachability
checks.
The Router persists its generated HTTPS/IMAP certificate under `/data/tls`;
clients must trust that certificate or mount an operator-managed certificate
and key. Keep the existing `/data` volume and `gateway.env` together when
changing images so account tokens and encrypted Planner resources remain
readable.
First, an operator sets `PUT /email/receiving-settings` over HTTPS with the
registry token and `{"mx_host":"mx.gateway.example"}`. This Planner resource
is the physical public SMTP hostname shown in customer DNS instructions, not
an environment variable. Publish A/AAAA for it and make TCP 25 reach Spinner.
An optional `forwarding_domain` may be set only after it is verified and
enabled under the operator's `/email/domains` API.

For a customer-owned domain, create `POST .../receiving-domains` with
`{"domain":"acme.example"}`. The response has a TXT ownership challenge and an MX instruction (priority 10, the
operator-configured host). Publish the TXT record, then call
`POST .../receiving-domains/{id}/verify`. `mx_status` is a separate DNS
observation: `pending`, `ready`, `unavailable`, or `unconfigured`; it is not
proof that the SMTP port is reachable. `ready` requires the observed MX set to
contain only the instructed host at priority 10; a more-preferred old MX keeps
the status pending. Unverified domain claims are account-scoped and do not
reserve the domain against another account; the first account that proves its
TXT challenge acquires the globally exclusive receiving domain. Verification
and route enabling do not
write customer DNS. Gateway rechecks TXT ownership according to the Email
runtime policy's verification TTL. A missing record fails closed.

The application building on Gateway sets the customer-facing TXT format through
the registry-authenticated `POST /email/verification-profile` with, for example,
`{"label":"_zorvia-verify","value_prefix":"zorvia-verification="}`. Read it with
`GET /email/verification-profile`; update it with `PUT` and the current
`If-Match` revision. The label must be one underscored DNS label and the value
prefix a short ASCII key ending in `=`. Gateway appends a random token. New
receiving, managed-sending, and legacy forwarding domains snapshot this format.
Existing domains without a snapshot continue to use `_gateway-verify` and
`gateway-verification=`. Changing the operator profile does not alter their
DNS instructions or periodic verification.

To move an already verified account domain, read
`GET .../{receiving-domains|sending-domains}/{id}/migration-record`, publish
its proposed TXT record, then call `POST .../{receiving-domains|sending-domains}/{id}/migrate`.
Gateway checks the new DNS record before switching, and updates both receiving
and managed-sending resources together when the same account owns both. Both
resources must already be verified. After migration, refresh cached domain
resources in the application before showing setup instructions; customers may
remove the old TXT record only after the new format is shown and verifies.
Operator forwarding domains use the same sequence at
`/email/domains/{id}/migration-record` and `/email/domains/{id}/migrate` with
the registry token.

### Managed receiving ingress

The account API uses `mode: "managed"` for new direct MX and Worker-backed
receiving routes. Gateway selects the physical adapter when the receiving
domain is created and saves that choice on the domain. A later settings change
affects new domains only. With no explicit selection, the adapter is `mx`.
The registry-authenticated `PUT /email/receiving-settings` may include:

```json
{"mx_host":"mx.gateway.example","managed_ingress":{"adapter":"worker","records":{"mx":{"name":"{domain}","value":"routing.example","priority":10},"cname":{"name":"mail.{domain}","value":"routing.example"}}}}
```

`adapter` accepts `mx` or `worker`. The Worker adapter requires operator
supplied DNS records. Record keys other than `txt` may represent any DNS
record type; each record contains `name`, `value`, and optional integer
`priority`. A label such as `provider_txt` must include `"type":"TXT"`, so
multiple TXT instructions can be rendered without ambiguity. `{domain}` is
replaced with the customer domain. Gateway adds its
TXT ownership challenge under `records.txt`. The account receives only these
DNS records, generic route status, and its callback details. `mx_status` is
the DNS observation for a returned MX record. Worker names, ingress URLs,
tokens, and provider credentials stay in Gateway's operator configuration.
The operator provisions a managed Worker token with the registry token at
`POST /email/operator/managed-worker-ingress/{account}/{route}/rotate` using an
empty JSON object. The one-time token must be installed as the Worker's secret;
account tokens cannot read or rotate it. A managed Worker route reports
`pending` until Gateway has a token, then `active`. An active status does not
prove that the external Worker is deployed or that DNS is ready.

Legacy `mx` and `worker` routes keep operating. To migrate a route without
changing its ID, address, enabled state, callback secret, or existing Worker
token, call account-authenticated `PATCH .../receiving-routes/{route}` with
`{"mode":"managed"}`. Repeating this request is safe. Gateway snapshots the
route's current physical adapter as internal configuration. Thus the existing
`support@sosongo.com` Worker route should become managed with a Worker adapter,
and `contact@rowingboat.ng` MX route should become managed with an MX adapter.
The account's `GET` route representation then reports `mode: "managed"`.
No DNS cutover is needed for this representation change.

For new customer-owned managed receiving, create an exact route with `POST .../receiving-routes`:

```json
{"mode":"managed","domain_id":"<verified domain UUID>","local_part":"support","callback_url":"https://app.example/inbound","network_scope":"public","external_id":"zorvia-connection-id","webhook_secret":"<64 lowercase hex characters>"}
```

The account may supply its own random webhook secret to make retries after a
lost create response recoverable. Without one, Gateway generates and returns
a secret only on create; GET never returns it. `PATCH .../receiving-routes/{id}`
accepts only `{"enabled":false}` or `{"enabled":true}`. A disabled exact
address rejects SMTP rather than falling through to a catch-all or other
route. Unknown addresses at a claimed customer domain also reject. Only one
recipient is accepted per SMTP transaction for these routes.

Alternatively, create a forwarding route with `mode: "forward"`, an
`external_id` stable within the account, a client-generated 64-character hex
`webhook_secret`, and `callback_url`; omit `domain_id` and `local_part`.
Gateway deterministically assigns an opaque `z-...@<forwarding_domain>`
address. Retrying with the same external ID, callback URL, and secret returns
the same route; altered retry details are rejected. A customer can forward
their existing mailbox to this address without changing MX. The operator
must publish MX for the forwarding domain separately.

Gateway returns `250` to SMTP DATA only after its durable queue accepts the
message. The queued HTTP delivery uses `Content-Type: message/rfc822` with
the original MIME bytes. It sends `X-Gateway-Route-Id`,
`X-Gateway-Event-Id`, and `X-Gateway-Envelope` (base64 JSON with `mail_from`
and one `recipients` entry). `X-Gateway-Signature` is `sha256=` plus the
lowercase hex HMAC-SHA256 keyed by the literal webhook secret over:

```text
route_id + "\n" + event_id + "\n" + envelope_header + "\n" + lowercase_hex_sha256(raw_mime)
```

The application must verify the signature over the exact body, authorize
the route ID to its own tenant, persist MIME safely before returning 2xx,
and deduplicate by route ID and event ID. Delivery is at least once; 5xx and
transient network errors retry. Gateway's outbound HTTP policy blocks private
and loopback callback addresses by default. For an intentional private
Compose/Kubernetes callback, the operator adds an exact `url` and private
IPv4 `cidrs` list to `private_callbacks` in `/email/receiving-settings`, then
sets a hard deployment ceiling with `GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS`
(for example the narrow Compose subnet). The account route explicitly asks
for `"network_scope":"private"`; Planner grants only the exact approved URL
and Spinner pins the resolved destination to the intersection of that grant
and the deployment ceiling. No proxy or redirects are allowed on that path.
Removing the Planner grant stops future delivery. The SMTP-to-HTTPS regression
uses disposable loopback fixtures; it does not prove cross-container or
public-network reachability. The hard ceiling is a bootstrap safety limit,
not a customer-configurable mail route.

For Gateway-managed sending, create an account-scoped domain with
`POST .../sending-domains` and `{"domain":"acme.example"}`. Publish the
returned `records.txt` ownership challenge, then call
`POST .../sending-domains/{id}/verify`. This is independent of receiving:
it does not require an MX change or an external SMTP provider. If the same
account creates both a receiving and a sending domain for the same name,
the later resource reuses the existing ownership TXT value; each is still
verified separately. Another account receives a distinct
challenge, and an unverified claim does not reserve the name. The domain
response reports ownership separately from SPF and DKIM; the latter are
`pending` until the configured records are published. On successful ownership
verification, Gateway generates a 2048-bit RSA DKIM key (or uses an existing
operator key for that domain) and returns the public
`records.dkim` TXT value at `<selector>._domainkey.<domain>`. The private key
stays in Planner's protected resource store. The operator configures
`GATEWAY_EMAIL_MANAGED_DKIM_SIGNING_ENABLED=true`, the matching private DKIM
resolver on Spinner, and optionally `GATEWAY_EMAIL_MANAGED_DKIM_SELECTOR`.
If the chosen relay requires SPF, the operator declares its complete,
provider-neutral TXT value with `GATEWAY_EMAIL_MANAGED_SPF_VALUE`; Gateway
returns it as `records.spf`. The generic SMTP relay uses the customer's
`mail_from` address as its return path, so it declares no separate return-path
record. Gateway checks every declared DNS record before issuing a managed
sending route, before re-enabling one, and when authenticating it. DNS readiness
does not prove the upstream relay accepts that customer domain; the operator
must validate that relay capability separately. Verification alone does not
prove deliverability or sender reputation.

### Shared operator sender

For a hosted service that sends for customers who have not verified their own
sending domain, the Gateway deployment can set
`GATEWAY_EMAIL_SHARED_FROM_ADDRESS` to an operator-owned address already
authorized by its outbound relay. This is a deployment setting, independent of
the relay vendor. Each account creates `POST .../sending-routes` with
`{"mode":"shared"}` and receives one-time HTTP Basic submission credentials.
Gateway refuses to create, re-enable, or authenticate that route unless managed
DKIM signing is enabled, an active key exists for the address domain, its public
TXT record is published, and any declared SPF record is also published. An
existing route reports `pending` if that readiness later disappears.
The shared route does not claim a customer domain and cannot authenticate to
raw SMTP, where Gateway cannot enforce conversation Reply-To headers.

Before each conversation sends, create a `mode:forward` receiving route on the
operator's verified `forwarding_domain`, using a stable, unique `external_id`
for that conversation, a client-generated webhook secret, and the account's
callback URL. Submit through `POST /email/submissions` with the shared route's
credentials, `mail_from` equal to the configured operator address, plus
`sender_name` and `reply_route_id`. Gateway replaces the MIME `From` with the
support name and operator address and replaces `Reply-To` with that exact
account-owned forwarding address before queueing. The account must keep that
receiving route enabled for later replies. A channel-wide external ID routes
back to the channel only; use a distinct conversation ID when the reply address
itself must identify the conversation. Existing `Message-ID`, `In-Reply-To`,
and `References` headers remain in the MIME for the hosted service's own
threading logic, but Gateway does not interpret them to choose a conversation.
The hosted service must map its external ID to the intended conversation when
handling the signed inbound callback. Customer-domain and external-provider
sending modes continue to use their existing contracts.

Create `POST .../sending-routes` with a verified `domain_id` and `local_part`.
The exact sender address is owned by that account. The response returns the
route UUID as `smtp_client_username` and a one-time
`smtp_client_password`; `GET` never returns the password. Use those
credentials with HTTPS `/email/submissions` (Basic auth plus
`Idempotency-Key`) or TLS-gated SMTP AUTH. Gateway's existing durable
outbound queue and operator-configured relay handle delivery; a missing
outbound adapter yields a service-unavailable response rather than an
accepted receipt. `PATCH .../sending-routes/{id}` enables or disables a
route, `POST .../{id}/rotate` changes the password, and deletion revokes it.
The operator must configure a relay, SPF, DKIM, TLS, abuse limits, and
public reachability before treating this as a production sending service.
No Cloudflare or Resend credential is part of this account-facing contract.
Gateway does not register customer sender identities with Resend, SES, or any
other delivery provider. The application building on Gateway owns that provider
credential, domain-registration workflow, provider identifier, and verification
polling. It publishes only provider-neutral DNS instructions and readiness to
its customer, then configures Gateway's generic outbound transport and domain
readiness resources after the provider confirms authorization. Changing
providers therefore changes the application adapter and transport resources,
not Gateway's managed-domain or submission contract.

For sending through a customer's existing external SMTP provider instead,
use `POST .../sending-connections` with `address`,
`smtp_upstream_id`, `smtp_username`, `smtp_auth`, and `smtp_secret`. The SMTP
upstream itself is an operator-managed `/email/upstreams` resource. The create
response gives a one-time `smtp_client_password`; the connection UUID is the
client username. `POST .../{id}/test` probes SMTP AUTH without sending a
message, and `PATCH .../{id}` with `{"enabled":true}` requires a healthy
probe. The existing HTTPS `/email/submissions` endpoint then accepts Basic
authentication and `Idempotency-Key` for durable outbound mail. No IMAP
upstream, login, or MX change is required. Real-provider submission behavior
is not established by the local tests. `PUT .../sending-connections/{id}`
replaces SMTP upstream credentials, disables the connection, and requires a
new test; `POST .../{id}/rotate` issues a new one-time client password if the
create response was lost or the client credential needs rotation.

Gateway DNSHook can serve MX answers only for a zone actually delegated to
Gateway's authoritative DNS listeners, or via a separately configured
provider integration. The account receiving API currently returns DNS
instructions; it does not grant tenants DNSHook administration or mutate
external customer zones. A Zorvia-managed DNS wizard should treat this as a
manual/provider-specific step until an account-scoped DNS record API exists.

## MX mail hooks

Mail hooks are an opt-in MX receiving path alongside alias-to-SMTP forwarding
and external IMAP mailbox connections. They require an active hook resource,
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

### Planner-managed transport setup

For a new local deployment, run the shared `configurations` migration
and create IMAP, POP3, and mailbox SMTP upstreams through
`POST /email/upstreams`. Create hosted adapters and routes through
`POST /email/hosted-adapters` and `POST /email/hosted-routes`, and import each
DKIM domain/selector/private key through `POST /email/dkim-keys`. For SRS,
create `/email/srs-policy` with a Gateway-controlled return domain and stable
verification keys before accepting forwarded mail or bounces.
Old Mail transport JSON maps, DKIM file manifest, and SRS env values are no
longer read. A missing or disabled Planner resource fails closed. Verify one
connection, hosted delivery, signing, and SRS bounce locally before directing
public MX or submission traffic to the deployment.

These are local implementation contracts, not evidence of compatibility with
real Google/Microsoft mailboxes or a production MX/relay deployment.

Mailbox connections are a separate opt-in path for customers whose mail lives
on an external IMAP/SMTP provider. Run the Planner migrations, which create
`accounts`, `configurations`, `configuration_secrets`, and
`resource_observations`.
Connection definitions are account-owned `email.mailbox-connection` resources;
live credentials and health observations are stored separately. Create
`POST /email/upstreams` resources for each IMAP and SMTP ID with `protocol`,
`name`, `address`, and `server_name`; SMTP also requires `tls_mode`
(`implicit` or `starttls`). `network_scope` defaults to `public`: Spinner
resolves the hostname, rejects private and special-purpose IPs, and dials a
checked IP without a second DNS lookup. An operator may set `network_scope`
to `private` for an intentionally internal mail server; loopback and
link-local addresses remain blocked. No exact-host environment list is
required. Set
`GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR` to
`Webong\Gateway\Email\ConnectedIMAPAuthenticator` and
`GATEWAY_EMAIL_SUBMISSION_AUTHENTICATOR` to
`Webong\Gateway\Email\ConnectedSMTPAuthenticator`. Enable existing IMAP and
HTTPS submission listeners, create `/email/runtime-policy` with
`connected_smtp_enabled: true`,
and give Spinner durable delivery storage, `GATEWAY_INTERNAL_TOKEN`,
`GATEWAY_MAILBOX_SMTP_RESOLVER_URL` pointing to Planner's private origin, and
a private delivery admin listener. The Caddy SMTP app supports the equivalent
`mailbox_resolver_url` setting.

An operator creates an account with `POST /accounts` using the
registry token and `{"id":"customer-id"}`. Mail currently uses the returned
account bearer token at `/email/mailbox-accounts/{account}/connections`; other
resource kinds must authorize access explicitly. Create a connection with an
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

For receive-only access to a customer's existing mailbox, use
`POST /email/mailbox-accounts/{account}/imap-connections` instead. The exact
create body is `address`, `imap_upstream_id`, `imap_username`, `imap_auth`,
and `imap_secret`; no SMTP upstream or SMTP secret is accepted. The response
contains `connection` (`id`, `account_id`, `mode: "imap"`, `address`, IMAP
settings, `enabled: false`, `health: "untested"`) and a one-time
`imap_client_password`. The UUID is the client IMAP username. `GET` never
returns either secret. `POST .../{id}/test` probes only IMAP, and
`PATCH .../{id}` with `{"enabled":true}` requires a healthy IMAP observation.
`PUT` replaces the IMAP settings and disables the connection; `POST
.../{id}/rotate` replaces only the client password. This mode has no SMTP
client password, cannot authenticate to SMTP or HTTPS submission, and cannot
resolve an outbound SMTP transport. The existing combined `/connections`
and SMTP-only `/sending-connections` remain unchanged. Pair IMAP-only receive
with a separate Gateway `/sending-routes` identity to send replies without
using the mailbox provider's SMTP service.

### Credential ownership modes

Connections now declare a credential mode. Existing connections and the
`/oauth/start` flow use `gateway_managed`: Gateway keeps provider refresh
credentials encrypted, refreshes them, and resolves a transient XOAUTH2
access token at use time. Password connections also use this mode.

Any account may instead use `application_managed` without giving Gateway a
provider refresh token. Register an account-owned credential source first:

```http
POST /email/mailbox-accounts/customer-id/credential-sources
Authorization: Bearer <Gateway account token>
Content-Type: application/json

{"name":"customer-app","url":"https://credentials.example.com/gateway/grants","authorization":"<random client-issued bearer token, at least 32 characters>"}
```

The response contains `id`, `name`, `url`, `adapter`, `enabled`, and `revision`, but never
the bearer token. The source URL must be HTTPS on port 443 with a DNS name;
Gateway denies loopback/private/special-purpose answers, pins the checked IP
for the TLS request, verifies the hostname, does not follow redirects or use
proxies, and applies a four-second total HTTP timeout. `GET` lists the
account's sources; `DELETE .../credential-sources/{id}` revokes a source and
causes dependent connections to fail closed. `PUT .../credential-sources/{id}`
with the full registration body rotates its bearer credential or URL without
changing dependent connection IDs. Register only a dedicated origin
that the account controls. The token authenticates Gateway to that origin; it
is not a Google/Microsoft token. Omitted `adapter` selects the built-in `http`
adapter and retains the POST contract below.

An application needing another request format installs an
`ApplicationMailboxGrantSource` implementation in the Planner deployment and
adds a stable key to `gateway.email.credential_source_adapters`. This is
deployment code, not a class name or arbitrary script supplied through the
account API. For example, a host may register `client_setup` to its own
adapter class. The client then includes `"adapter":"client_setup"` in the
source registration (and in a full `PUT` replacement). Unknown adapter keys
are rejected. Gateway selects the installed adapter per source and passes its
ID, account ID, connection ID, protocol, and mailbox address to `resolve`.
The adapter reads the account-owned URL and bearer from
`MailboxCredentialSourceStore::active`, performs its bounded authenticated
request, and returns the grant shape below. Gateway validates the grant before
IMAP/SMTP use and contains adapter exceptions without exposing remote bodies.
An adapter must enforce HTTPS, egress/SSRF policy, TLS verification, deadlines,
and response-size limits; installing one is an operator-level trust decision.
No client route path, HTTP method, headers, or request body is assumed by this
interface.

Create an IMAP-only connection at `/imap-connections`, an SMTP-only connection
at `/sending-connections`, or a combined connection at `/connections`. For a
combined application-managed connection, the exact create body is:

```json
{"address":"mail@example.com","imap_upstream_id":"imap-a","smtp_upstream_id":"smtp-a","imap_username":"mail@example.com","smtp_username":"mail@example.com","imap_auth":"xoauth2","smtp_auth":"xoauth2","credential_mode":"application_managed","credential_source_id":"<registered source UUID>"}
```

For IMAP-only or SMTP-only, omit the other side's three fields. No provider
secret is accepted in this mode. Gateway still returns one-time client
passwords for its IMAP/SMTP front door, and the normal test/enable/poll/send
lifecycle applies. At each test, scheduled poll claim, IMAP login, or queued
SMTP delivery, the built-in `http` adapter POSTs once to the registered source with:

```http
Authorization: Bearer <registered client-issued bearer token>
Content-Type: application/json
Accept: application/json

{"account_id":"customer-id","connection_id":"<Gateway connection UUID>","protocol":"imap","address":"mail@example.com"}
```

The source must answer HTTP 200 with exactly:

```json
{"account_id":"customer-id","connection_id":"<Gateway connection UUID>","address":"mail@example.com","access_token":"<short-lived XOAUTH2 access token>","expires_at":1792638000,"capabilities":["imap","smtp"]}
```

`expires_at` is a Unix timestamp. It must be more than 30 seconds away (more
than 120 seconds for a scheduled IMAP poll) and no more than one hour away.
Gateway rejects an account, connection, mailbox identity, capability, expiry,
or response-shape mismatch. It never writes this access token into Planner
resources, secrets, schedules, or queued deliveries. The client application
owns consent, refresh, rotation, and revocation; Gateway owns only transport
execution and its opaque connection/client-login IDs. Other applications can
implement the same callback without knowing any provider-specific Gateway
details. The source must answer quickly and avoid returning provider error
bodies or refresh credentials.

A scripted source can instead shape the request for any application's API.
The source keeps its registered URL and bearer secret, while the script sees
only the connection context and emits exactly one POST, PUT, or PATCH request:

```json
{"name":"application-primary","adapter":"script","language":"javascript","url":"https://app.example.com/api/connect/connections/setup","authorization":"<connection-owned bearer>","source":"gateway.forward({url: gateway.event.source_url, method: 'PATCH', headers: {'Content-Type': ['application/json']}, body: JSON.stringify({context: gateway.event})});"}
```

JavaScript and Lua execute in Spinner; WebhookScript executes in Planner. All
three receive `account_id`, `connection_id`, `protocol`, `address`, and
`source_url`. WebhookScript reads these as `credential.*` variables and calls
`forward(...)`. Gateway requires the emitted URL to equal the registered URL,
rejects credential and routing headers supplied by scripts, injects the
encrypted bearer after script execution, and applies the same public-network,
TLS, response-size, timeout, and grant-shape checks as the built-in `http`
adapter. Scripts never receive the bearer or the returned access token.

For Gateway-managed customer OAuth consent, configure a fixed HTTPS
`GATEWAY_EMAIL_OAUTH_CALLBACK_URL` handled by the client application, the matching
`GATEWAY_EMAIL_GOOGLE_CLIENT_ID`/`GATEWAY_EMAIL_GOOGLE_CLIENT_SECRET` or
`GATEWAY_EMAIL_MICROSOFT_CLIENT_ID`/`GATEWAY_EMAIL_MICROSOFT_CLIENT_SECRET`,
and optional `GATEWAY_EMAIL_MICROSOFT_TENANT` (default `common`). Register
operator-approved upstreams and set the matching provider's
`GATEWAY_EMAIL_{GOOGLE|MICROSOFT}_{IMAP|SMTP}_UPSTREAM` IDs (defaults are
`google-imap`, `google-smtp`, `microsoft-imap`, `microsoft-smtp`). Gateway
selects these IDs; the client never supplies provider hosts, client secrets, or
refresh tokens for this flow.

`POST /email/mailbox-accounts/{account}/oauth/start` over HTTPS with the
account bearer token and body:

```json
{"provider":"google","mode":"imap","address":"person@example.com","sending":false}
```

For combined IMAP and SMTP, use `mode: "mailbox"` and `sending: true`. To
reauthorize an existing connection, include its `connection_id`; it must
belong to the account and have the same mode. The response is
`authorization_url`, opaque `state`, and `expires_at`. Gateway fixes the
callback URL and creates a 10-minute, single-use account-bound PKCE
transaction. Google requests offline consent and `https://mail.google.com/`;
Microsoft requests `offline_access` and the delegated Exchange IMAP scope,
plus SMTP scope for combined mode.

The client application's callback handler forwards only `provider`, `state`, `code` (or
`error`), and its exact `callback_url` to account-authenticated HTTPS
`POST /email/mailbox-accounts/{account}/oauth/complete`. Gateway exchanges
the code, stores the refresh token encrypted, tests the selected transports,
and enables the connection. Success returns `connection` plus one-time
`imap_client_password` and, for combined mode, `smtp_client_password`; an
existing connection keeps its client passwords and returns null password
fields. A repeat with the same state and code returns the same result until
the transaction expires; a different code or account cannot reuse it.
Provider failures produce a generic error without forwarding provider bodies.
For Google, Gateway also reads the Gmail profile with the new access token and
requires its mailbox address to match the expected address. For Microsoft,
the IMAP credential probe must authenticate as the expected mailbox address;
an ID-token email claim is not used as proof of mailbox access.
The existing prune command deletes expired transactions, including their
short-lived encrypted completion metadata.

`GATEWAY_EMAIL_OAUTH_REFRESHER` defaults to
`Webong\Gateway\Email\ProviderMailboxOAuthRefresher` and refreshes at fixed
provider endpoints during IMAP/SMTP use. A combined connection keeps both
sides synchronized when one shared refresh token rotates. Queued messages
store only the connection UUID, never OAuth tokens. Google and Microsoft
real-provider compatibility still needs authorized live tests, including
token rotation, revocation, and tenant permissions. The credential probe does
not guarantee that a provider will accept later message DATA.

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
starts. The stock runtime instead resolves Planner-managed HTTPS provider
adapters and exact-recipient routes on each attempt. Provider secrets remain
encrypted in Planner and are constrained by deployed allowed hosts. See
[Hosted provider contract](hosted-mail-provider.md).

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

The Planner-owned control plane stores Email domains and aliases as typed,
versioned `email.domain` and `email.alias` resources in `configurations`. The
`REGISTRY_TOKEN`-authenticated `/email/domains` API creates
a disabled domain and returns a TXT challenge using the configured operator
format (legacy default `_gateway-verify.<domain>`). `POST /email/domains/{id}/verify` checks that
record on every call; enabling a disabled domain rechecks live TXT ownership
before accepting mail. A missing record or unavailable DNS lookup leaves the
domain disabled. For an enabled domain, Planner rechecks TXT ownership on the
next SMTP recipient or DATA decision once `verified_at` is older than
`verification_ttl_seconds` in `/email/runtime-policy` (default 86400 seconds; `0` checks
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
GATEWAY_EMAIL_QUARANTINE_PATH=/var/mod/gateway/email-quarantine
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
GOWORK=off go -C pkg/spinner run -mod=mod ./cmd/email-quarantine list -path /var/mod/gateway/email-quarantine
GOWORK=off go -C pkg/spinner run -mod=mod ./cmd/email-quarantine show -path /var/mod/gateway/email-quarantine -id ID
GOWORK=off go -C pkg/spinner run -mod=mod ./cmd/email-quarantine export -path /var/mod/gateway/email-quarantine -id ID -output /absolute/review.eml
GOWORK=off go -C pkg/spinner run -mod=mod ./cmd/email-quarantine delete -path /var/mod/gateway/email-quarantine -id ID
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
GATEWAY_TEST_CLAMD_ADDR=127.0.0.1:13310 GOWORK=off go -C pkg/spinner test -mod=mod ./bridge/smtp -run 'TestClamdScannerAgainstRealService|TestSMTPRealClamdQuarantine' -count=1 -v
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

Per-domain outbound DKIM signing is opt-in. Create a protected
`POST /email/dkim-keys` resource with `domain`, `selector`, and
`private_key_pem`; public reads return only selector and key presence. Configure
`GATEWAY_EMAIL_DKIM_RESOLVER_URL` to Planner's private origin (or
`dkim_resolver_url` in Caddy). The private key must be PKCS#8 Ed25519 or
PKCS#8/PKCS#1 RSA PEM; RSA keys must be at least 2048 bits. Publish the
corresponding TXT public key at `mail1._domainkey.example.com` before sending.
Spinner, not Planner or the SMTP client, marks the authorized RFC 5322 author
domain. The outbound adapter signs the final message bytes immediately before
relay submission, including on durable-queue retries. If signing is configured
but that author domain has no key, authenticated SMTP submission is deferred
before accepting DATA rather than queueing mail that cannot be signed. The
adapter also refuses such a delivery if it reaches the queue by another path.
With no private signing resolver, existing submission remains unsigned.
Rotate selectors by publishing the new DNS key before replacing the Planner
resource. No Spinner restart is needed; retain the old public key until
previously sent mail has cleared. The private key is resolved over authenticated
private HTTP for signing and never enters the delivery queue or a generated file.

For shared submission quotas, create `POST /email/submission-policy` with
`daily_limit`, `daily_byte_limit`, `domain_daily_limit`, and
`domain_daily_byte_limit` (integer values; omitted fields default to `0`,
disabled). Use `GET` for the current revision and `PUT` or `DELETE` with its
ETag as `If-Match` to change or remove it. All Planner workers read this
shared resource; the former `GATEWAY_EMAIL_SUBMISSION_*_LIMIT` env settings
are no longer read. An authenticator may return an
`EmailSubmissionGrant` with a positive `dailyLimit` to override it for one
account. Spinner carries the canonical `auth_identity` and approved limit
from AUTH to each SMTP transaction; the username spelling supplied by a client
is never the quota key. Planner atomically increments one hashed-identity row
in its shared database per UTC day and replies with SMTP 452 once the limit is
reached. Set `daily_byte_limit` for an independent per-account cap on raw SMTP DATA bytes, or let the
authenticator return `dailyByteLimit` for an account-specific override (up to
1 TB). The same atomic update charges both attempt and byte usage before route
planning. A message rejected by either quota is charged to neither; a message
admitted by the quotas counts even if later policy or relay delivery fails.
Set `domain_daily_limit` and/or `domain_daily_byte_limit` for independent UTC-day
caps shared by **all authenticated accounts** sending from the same
case-insensitive envelope-sender domain. Each cap defaults to `0` (disabled).
Planner updates account and domain counters in one transaction: reaching
either cap rejects the message without charging the other counter. Domain
caps apply to SMTP AUTH and HTTP submission alike, but not to unauthenticated
inbound mail. All Planner workers must use the same database and quota
policy; install the domain-usage migration before enabling the caps.
These are abuse-control limits, not counts of delivered mail. Run
`php artisan gateway:prune-email-submission-usage` periodically to remove
rows older than `submission_usage_retention_days` in `/email/runtime-policy` (default 90).
The limits require the Planner usage, byte-column, and domain-usage migrations
and a shared database across workers;
a database failure defers submission rather than bypassing the quota.

Set `http_submission_enabled: true` in `/email/runtime-policy` to opt in to HTTP
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
`GATEWAY_POP3_TLS_CERT_FILE`, and `GATEWAY_POP3_TLS_KEY_FILE`.
Create a `POST /email/upstreams` resource for each POP3 ID.
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
`GATEWAY_IMAP_TLS_CERT_FILE`, and `GATEWAY_IMAP_TLS_KEY_FILE`, then create IMAP upstream resources
pointing to native implicit-TLS IMAP servers. Optional
limits are `GATEWAY_IMAP_MAX_CONNECTIONS`, `GATEWAY_IMAP_AUTH_PER_MINUTE`,
`GATEWAY_IMAP_AUTH_BURST`, `GATEWAY_IMAP_IDLE_TIMEOUT` (minimum ten minutes),
`GATEWAY_IMAP_PLANNER_TIMEOUT`, and `GATEWAY_IMAP_UPSTREAM_TIMEOUT`.

The custom FrankenPHP/Caddy build also registers `gateway.pop3` and
`gateway.imap` sibling TCP apps. They use the same TLS-only POP3/IMAP servers
and private HTTP Planner bridge as standalone Spinner. Configure either app
in the Caddy global block with `listen`, `planner_url`, `internal_token`,
`tls_cert_file` and `tls_key_file`. For example:

```caddyfile
{
    gateway_pop3 {
        listen :995
        planner_url http://127.0.0.1:8081
        internal_token {$GATEWAY_INTERNAL_TOKEN}
        tls_cert_file /etc/gateway/mail.crt
        tls_key_file /etc/gateway/mail.key
    }
    gateway_imap {
        listen :993
        planner_url http://127.0.0.1:8081
        internal_token {$GATEWAY_INTERNAL_TOKEN}
        tls_cert_file /etc/gateway/mail.crt
        tls_key_file /etc/gateway/mail.key
    }
}
```

The `planner_url` must be a private PHP-only listener as in the
[FrankenPHP example](../infra/caddy/Caddyfile.frankenphp.example); never expose
it publicly. Optional Caddyfile controls are `hostname`, `max_connections`,
`auth_per_minute`, `auth_burst`, `idle_timeout`, `planner_timeout`,
`upstream_timeout`, `max_body_size`, and `upstream_ca_file` for a private CA
certificate. Without that option the host trust store is used. The upstream
host ceiling is deployment-owned; Planner returns an approved endpoint with
the authenticated decision, and Spinner verifies its TLS certificate against
the resource's server name. Disabling a resource stops new logins.

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

`pkg/spinner/bridge/smtpout` implements authenticated relay submission with plaintext,
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

## Account mailbox polling (local implementation)

Gateway's bundled Router can poll an external IMAP mailbox without Zorvia
running its own IMAP worker. First create, test, and enable an account-owned
combined or IMAP-only connection. Then `PUT` over HTTPS with that account's
bearer token to `/email/mailbox-accounts/{account}/connections/{connection}/polling`
or `/email/mailbox-accounts/{account}/imap-connections/{connection}/polling`:

```json
{
  "callback_url": "https://zorvia.example.com/webhooks/gateway-mail",
  "webhook_secret": "64-hex-character-shared-secret",
  "enabled": true
}
```

The callback must be a public HTTPS URL without credentials, query, or
fragment. The response has a stable `route_id`; `GET` returns its current
`uid_validity` and `last_uid` checkpoint without the secret. `DELETE` removes
the poll schedule and checkpoint. The operator can disable polling by sending
`enabled: false` in another complete `PUT`. The secret is encrypted in the
Planner resource and never returned by `GET`. There is no mailbox-polling
environment toggle in the bundled image; the shared schedule loop starts by
default and stays idle until a poll resource is enabled.

The account-owned response also includes `status` with `state`
(`disabled`, `awaiting_first_run`, `running`, `retry_pending`, `healthy`, or
`failed`) and UTC `last_run_at`, `last_success_at`, `last_failure_at`, and
`next_run_at` timestamps. These fields summarize the poll schedule and its
latest runs. A successful run includes an empty mailbox; `healthy` does not
prove that new messages arrived. Failure details, upstream addresses, and
credentials are not returned. The schedule can fall behind without a new run,
so consumers should also compare `next_run_at` with the current time.

Planner creates a built-in `native.imap` schedule every minute. Spinner claims
that schedule, then obtains a separate per-resource lease and fresh IMAP
credentials through the private in-process Planner interface. It selects
`INBOX` read-only, searches UIDs after its durable checkpoint, and fetches at
most three messages of at most 10 MiB each per run. It sends each raw MIME
message to the callback as `message/rfc822`. The callback headers are
`X-Gateway-Route-Id`, deterministic `X-Gateway-Event-Id`, base64-JSON
`X-Gateway-Envelope`, and `X-Gateway-Signature` using the same HMAC-SHA256
contract as Gateway MX receiving. For IMAP, `mail_from` is derived from the
message's `From` header, not an SMTP envelope; `recipients` contains the
connected mailbox address. The callback must authenticate the signature and
persist the event before returning 2xx.

Only a 2xx callback advances the UID checkpoint. A failure leaves the UID
pending for the next scheduled run, which retries with the same event ID.
Zorvia must deduplicate by route ID plus event ID, register the poll's
`route_id` with its Gateway-mail callback, and permit its mailhook connection
to import that callback. A changed IMAP UIDVALIDITY resets the UID cursor and
changes event IDs. This is local code, not a published image or a
provider-interoperability claim; OAuth, mailbox-size, and real-provider
behavior still require deployment tests.

## Cloudflare Email Worker ingress (local implementation)

When Cloudflare retains MX for a domain, Gateway can accept the raw message
over HTTPS instead of receiving the SMTP transaction itself. This is a
separate `worker` receiving-route mode; it does not require Gateway's MX
hostname or DNSHook delegation. The account must still verify ownership via
Gateway's `_gateway-verify` TXT challenge. Create the exact route with
`mode: "worker"`, `domain_id`, `local_part`, `callback_url`, and
`webhook_secret`. An existing verified MX route can instead switch in place
using account-authenticated `PATCH .../receiving-routes/{route_id}` with
`{"mode":"worker"}`. The route ID, address, callback, secret, and enabled
state are retained; Gateway SMTP acceptance stops for that route. The switch
can be reversed with `PATCH .../receiving-routes/{route_id}` and
`{"mode":"mx"}` while the receiving domain is verified and enabled and an
operator MX hostname is configured. The route ID, callback, secret, enabled
state, and dormant Worker ingress token remain intact; Gateway SMTP accepts
the address again. The Cloudflare Email Routing rule still needs an explicit
cutover. Do not point both paths at the same inbox without application-side
event deduplication.

With the account bearer token, `POST` an empty JSON object to
`/email/mailbox-accounts/{account}/receiving-routes/{route_id}/worker-ingress`.
The response returns a one-time `ingress_token`; calling it again rotates and
invalidates the old token. `GET` reports the credential's existence without
returning the token. Deleting the receiving route removes its credential.
Deploy `infra/cloudflare/email-worker.mjs` as a Cloudflare Email Routing
Worker using `infra/cloudflare/wrangler.jsonc.example` as the configuration
template. Set `GATEWAY_RECIPIENT` to the exact routed address and
`GATEWAY_INGRESS_URL` to the public HTTPS URL
`https://<gateway-host>/email/worker-ingress/{route_id}`. Store
`GATEWAY_INGRESS_TOKEN` as a Worker secret, not a plain `vars` value.

The Worker posts the original `message.raw` bytes as `message/rfc822`, with
`Authorization: Bearer <ingress_token>`, `X-Gateway-Mail-From` and
`X-Gateway-Recipient`. Gateway requires TLS, checks the token, verified active
route and exact recipient through Planner, and replies `202` only after its
delivery queue durably records the signed HTTPS callback to Zorvia. Repeated
requests for the same route, envelope and MIME bytes are idempotent while the
queue receipt is retained. Gateway bounds a message at 10 MiB. The callback
signature and at-least-once delivery semantics are the same as the MX route.
The Worker does not hold a backup queue: on anything other than `202` it calls
Cloudflare's `setReject`, so the sender gets a visible rejection rather than a
false acceptance. Check Cloudflare's routing and rejection behavior with a
real Gmail-to-domain message before treating this as deployed.
