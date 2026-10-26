# Webong DNS

Gateway Extension for managed authoritative DNS, application-provided answers,
and policy-scoped forwarding to upstream resolvers.

The Composer entrypoint contributes Planner zones, policy, and management APIs.
The Go `spinner` package owns UDP/TCP DNS serving and the private recursive
resolver capability used by other Extensions. Its installable
`cmd/spinner` process receives listener limits from Planner's revisioned
`/extensions` resource. The DNS Planner entrypoint projects active `/dns/zones`
resources into the same integrity-bound runtime snapshot, so Spinner core does
not import DNS, parse DNS environment variables, or maintain a second zone
catalog. Updating either source replaces the process with the new validated
snapshot. Both entrypoints are released as one Extension identity and version.

## Answer modes

- **Managed DNS**: Gateway answers a Planner-managed zone from its records.
- **Provided DNS**: Gateway remains authoritative for a Planner-managed zone,
  but obtains a dynamic answer through the normal synchronous reply Interface.
  The reply can be an HTTP Webhook or an installed Automation or Integration;
  each returns the same bounded DNS answer document.
- **Forwarded DNS**: when no managed zone matches, Gateway may proxy the query
  to an upstream resolver selected by longest domain suffix and most-specific
  permitted client network. Gateway does not expose general recursion on the
  public authoritative listener, cache answers, or perform DNSSEC validation;
  the forwarded resolver owns those behaviors.

## Extension resolver capability

DNS provides the private `gateway.dns/v1 exchange` capability to other verified
Gateway Extensions. It accepts one bounded standard IN query, rejects transfer
and ANY queries, resolves over UDP with TCP fallback for truncated responses,
and validates that the response matches the query. The resolver uses an
explicit IP-and-port `resolver_address` or the host's `/etc/resolv.conf`, with a
bounded `resolver_timeout_ms`.

This capability is independent of authoritative serving. A DNS process with no
public `address`, zones, or forwarders runs resolver-only. `webong/email`
declares the capability as a package dependency; Spinner resolves the active
package graph, starts DNS first, and binds Email to the exact selected DNS
revision. DNS and Email remain separate modules and neither imports the other.

Forwarded DNS is configured through Planner rather than environment variables:

```http
POST /dns/forwarders
Authorization: Bearer <registry token>
Content-Type: application/json

{
  "name": "application",
  "address": "resolver.internal:53",
  "transport": "udp",
  "domains": ["internal.example", "."],
  "client_cidrs": ["10.40.0.0/16"],
  "timeout_ms": 750,
  "enabled": true
}
```

`domains` contains one to 32 DNS suffixes; `"."` is an explicit catch-all.
`client_cidrs` is mandatory and the unspecified `0.0.0.0/0` and `::/0`
networks are rejected, so enabling Forwarded DNS does not implicitly create an
open resolver. UDP is the default upstream transport and automatically retries
a truncated response over TCP. Use `tcp` when the forwarded resolver requires it.

Planner exposes `GET`, `POST`, `GET /{id}`, `PUT /{id}`, and `DELETE /{id}` at
`/dns/forwarders`. Replacement and deletion require the current `ETag` in
`If-Match`. Active resources are projected into the revisioned `webong/dns`
runtime snapshot; Spinner validates the complete snapshot before starting.
The Extension independently enforces hard ceilings even when Planner asks for
more: Planner deadlines top out at 30 seconds, socket deadlines at 30 seconds,
responses at 256 records, query rate at 100,000 per second, and burst at
200,000. Infrastructure still controls whether the configured address is
reachable or published.

## DNS provider credentials

Cloudflare delegation is Planner-side Extension behavior and no longer reads a
token from an environment variable. Create an enabled `webong/dns` activation,
write a DNS-owned secret such as `cloudflare-primary` with
`{"api_token":"..."}`, then create the zone with `provisioner: "cloudflare"`
and `provisioner_options.credential_ref: "cloudflare-primary"`. The versioned
zone stores only the reference. Provision and deprovision resolve the current
token at execution time; a missing, disabled, malformed, or cross-Extension
secret fails closed.

## Provided answers

A DNS Endpoint is an ordinary Gateway route. Exactly one synchronous answer source
may match it:

- a `reply` subscription invokes its HTTP(S) Webhook and uses a successful
  response body as the answer document;
- an `automation` plan runs through the installed JavaScript, Lua, HookScript,
  or other declared language runtime and uses its successful response body;
- an Integration may select either contract without adding a DNS-specific
  callback mechanism.

Spinner advertises the Automation languages actually available in that runtime.
Planner fails closed when a selected language is unavailable. Spinner applies
the DNS deadline, validates the returned record types and count, and returns
`SERVFAIL` for an unavailable action or malformed result. Automation deliveries
and schedules retain their normal declarative behavior; only the Automation's
successful response becomes the DNS answer.

## Post-answer observations

Managed, Provided, and Forwarded queries emit the same versioned `dns.query` observation
after Spinner has written the selected response. The document contains bounded
question and answer summaries, routing mode, selected zone, endpoint, or forwarder,
response code, elapsed time, and a failure classification. It never contains a
raw DNS packet.

The Extension submits observations through a bounded, non-blocking queue and
the generic `gateway.observation/v1` Arranger interface. Queue saturation,
Planner unavailability, or an observer failure can drop telemetry, but cannot
replace or delay an already selected DNS answer. Planner fans a validated
observation out to the recorders registered for its type; the DNS package
registers shared history and usage aggregation without putting DNS knowledge in
Planner or Spinner core.

Managed Zone history and usage are available at:

- `GET /dns/zones/{id}/events`
- `GET /dns/zones/{id}/usage`

Provided Endpoint history and usage use the same event ledger:

- `GET /dns/endpoints/{id}/events`
- `GET /dns/endpoints/{id}/usage`

Forwarder history and usage use it as well:

- `GET /dns/forwarders/{id}/events`
- `GET /dns/forwarders/{id}/usage`

Additional analytics or notification recorders register for `dns.query` in
Planner. Recorders that need retries or durable external delivery should enqueue
that work through Gateway's delivery machinery instead of performing network
I/O in the recorder.
