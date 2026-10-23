# Email authentication and bounce-routing gate

Managed Email currently forwards through a configured SMTP relay. Spinner now
evaluates the envelope sender's SPF record against the actual SMTP TCP peer
before asking Planner for the DATA decision. A null reverse-path uses the HELO
identity instead. The `smtp`/`transaction` event includes `spf_result`
(`pass`, `fail`, `softfail`, `neutral`, `none`, `temperror`, `permerror`, or
`unavailable`), `spf_identity` (`mailfrom` or `helo`), and `spf_domain` when
one is available. A DNS lookup is bounded to three seconds. `unavailable`
means Spinner could not establish a trustworthy peer IP; it is never a pass.
Spinner also verifies up to five DKIM signatures against the original RFC 5322
bytes before forwarding adds its trace header. The same transaction event
includes `dkim_result` (`pass`, `fail`, `none`, `temperror`, or `permerror`) and
`dkim_signatures`, a JSON array of `{domain, result}` entries. Only a `pass`
entry asserts a verified signing domain; `dkim_truncated=true` means more than
five signatures were present. DKIM DNS lookups are bounded to three seconds.
Spinner also extracts one valid RFC 5322 From domain and the SPF and DKIM
identity domains. Planner performs a cached DNS tree walk of at most eight
queries per domain for policy discovery and relaxed alignment, including
delegated `psd=n` and public-suffix `psd=y` records. It applies `p`/`sp`,
strict or relaxed SPF/DKIM alignment, and the `t` testing policy downgrade.
It returns assessment metadata with managed forwarding
decisions. An aligned SPF
or verified DKIM pass satisfies DMARC; a temporary authentication or DNS
failure is not treated as a conclusive DMARC failure.

By default, this assessment does not alter mail acceptance. An operator can set
`dmarc_enforce_enabled: true` in `/email/runtime-policy` and configure
`GATEWAY_EMAIL_QUARANTINE_PATH` to durably quarantine conclusive DMARC failures
under `p=quarantine` or `p=reject` and temporarily defer uncertain assessments.
Testing mode (`t=y`) lowers `p=quarantine` to no DMARC disposition and
`p=reject` to quarantine. The obsolete `pct` tag is ignored. A trusted local
`EmailMessagePolicy` can still reject mail using additional analysis; the
sender's `p=reject` record alone does not cause a hard SMTP rejection. The
quarantine store and optional malware scanner are described in
[mail forwarding](mail-forwarding.md).

To apply `np` to non-existent author domains, configure a trusted recursive
resolver on Spinner:

```text
GATEWAY_EMAIL_DOMAIN_RESOLVER_ADDR=127.0.0.1:53
GATEWAY_EMAIL_DOMAIN_LOOKUP_TIMEOUT=3s
```

The Caddy SMTP app uses `domain_resolver_address` and
`domain_lookup_timeout`. Spinner queries the RFC 5322 From domain and sends
Planner `exists` for DNS NOERROR (including NODATA), `nxdomain` only for
NXDOMAIN, or `temperror` for a failed lookup. The resolver must be an
operator-controlled IP endpoint; do not point this at an untrusted DNS
service. If a discovered policy has `np` but this evidence is unavailable,
Planner temporarily defers a DMARC failure instead of guessing whether the
domain exists. This check is opt-in and off without a resolver address.

The switch still does not provide aggregate or failure reports.
Neither SPF nor DKIM evidence by itself rejects mail.
Keep public forwarding operator-controlled until the whole path below is
implemented and tested.

## Optional return-path rewriting

Planner can now issue an opaque, signed `SRS0-...` return address for managed
forwarding and persist its original sender, encrypted, in `srs_returns`. It
changes only outbound SMTP `MAIL FROM`; a null reverse-path stays null. Spinner
passes that envelope to the existing outbound relay. On a subsequent SMTP
`RCPT TO`, Planner reserves SRS-looking addresses ahead of catch-all aliases,
validates the token, mapping, and expiry, and accepts only a single return
recipient with a null reverse-path. DATA is checked again and relayed to the
original sender with `MAIL FROM:<>`. Invalid, expired, mixed, and non-null-sender
returns are rejected. This is a Gateway-owned, bounded opaque return token,
not a general decoder for other hosts' SRS addresses.

The feature is **off by default**. With the shared Planner database and a
stable Laravel `APP_KEY`, create one encrypted Planner resource (authenticated
registry operator request):

```json
POST /email/srs-policy
{"domain":"returns.example.com","keys":["<base64-encoded-32-byte-or-longer-secret>"],"max_age_seconds":1814400,"rewrite_enabled":true}
```

`GET /email/srs-policy` returns the current revision and key count, never the
keys. Use its ETag in `If-Match` for `PUT` or `DELETE`. The former
`GATEWAY_EMAIL_SRS_*` environment settings are no longer read; migrate their
values into this resource before upgrading an installation with active returns.

The return domain must be a verified, enabled managed Email domain; an
unavailable domain temporarily fails new forwarding rather than issuing an
unreachable return address. Its public MX must reach Gateway, and its SPF must
authorize the actual outbound relay—Gateway does not verify those records
automatically. Keep the old key in the list when rotating (`new` first, `old`
second) for at least the configured token lifetime. Keep the shared database
mapping for that same window. Expired mappings are pruned opportunistically
as new tokens are issued. Set `rewrite_enabled` or resource `enabled` to
`false` to stop issuing new addresses; keep the old keys and mappings until
the bounce window closes. Planner rejects removal of an old key, return-domain
changes, or deletion while unexpired returns are recorded. Test the live
MX/relay bounce path before accepting public traffic. Long-lived Planner
workers read this resource on each operation, so no worker restart is needed.

Before enabling rewriting, check the return domain against public DNS. Supply
**every** expected MX host and the actual public source IPs used by the
outbound relay (not Spinner's inbound IP):

```sh
go run -mod=readonly ./tools/email-dns-check \
  -domain returns.example.com \
  -mx-hosts mx1.example.com,mx2.example.com \
  -relay-ips 203.0.113.10,2001:db8::10
```

The command fails if the public MX set differs from the expected hosts or if
SPF does not return `pass` for every supplied relay IP. Replace the example
addresses with the relay's real egress addresses. This read-only check does
not prove that the MX hosts reach Gateway's SMTP listener, that the provider
uses only those egress IPs, or that a bounce is actually delivered; send a
live bounce through the public path as a separate deployment test. Repeat the
check after DNS or relay changes.

## Why SRS cannot be outbound-only

SRS changes the SMTP `MAIL FROM` reverse-path to a Gateway-controlled domain;
it does not change the visible RFC 5322 `From` header. A downstream failure
then sends a bounce to that new address. If Gateway cannot validate and reverse
it, the original sender will not receive the failure. If Gateway reverses an
untrusted address without a cryptographic check, it risks becoming a relay for
forged bounces. The [SRS design](https://www.libsrs2.net/srs/srs.pdf) specifies
both the forward and reverse paths; [Microsoft's operational documentation](https://learn.microsoft.com/en-us/exchange/reference/sender-rewriting-scheme)
also calls out the need for a bounce mailbox when rewriting.

## Required implementation sequence

1. Use the SPF evidence from the trusted TCP peer IP and SMTP envelope sender,
   DKIM verification from the unmodified RFC 5322 message, and Planner's DMARC
   assessment. Add reporting and a quarantine policy adapter before claiming
   full DMARC receiver behavior. An SPF failure alone must not cause a hard reject:
   aligned DKIM can still satisfy DMARC. A proxy that hides the sender IP cannot
   supply trustworthy SPF evidence without an explicitly trusted transport.
   Do not treat client-supplied `Received` or `Authentication-Results` headers
   as Gateway results. [SPF](https://www.rfc-editor.org/rfc/rfc7208.html),
   [DKIM](https://www.rfc-editor.org/rfc/rfc6376.html), and
   [DMARC](https://www.rfc-editor.org/rfc/rfc9989.html) are separate checks.
2. Choose and verify a Gateway-controlled SRS return domain. Its MX must
   deliver to Gateway and its SPF must authorize the actual outbound relay.
   Persist a stable signing key or a shared, durable token mapping before
   generating any SRS address. Define key rotation and expiration behavior.
3. Reserve SRS return addresses ahead of normal aliases, patterns, and
   catch-all matching. Validate the signature or token and expiry at `RCPT TO`.
   Invalid addresses must never fall through to a catch-all. Reject mixed
   ordinary and SRS recipients in one SMTP transaction.
4. Accept a bounce only through the SRS path with a null SMTP reverse-path,
   and relay it to the recovered original sender with a null reverse-path.
   Bound message size, attempts, and age; emit an auditable delivery record.
   Consider token leakage and bounce amplification before enabling this for
   public, unauthenticated senders.
5. Rewrite only the outbound SMTP envelope for managed forwarding. Preserve
   the original message headers and null reverse-path. Handle long local parts
   and nested SRS hops without producing an invalid SMTP address; fail
   temporarily if a safe rewrite is impossible.

## Acceptance tests before public enablement

- SPF, DKIM, and DMARC pass/fail/temporary-DNS cases, including IPv4, IPv6,
  null senders, forwarded mail, and a TCP proxy with an untrusted source.
- One valid SRS bounce returns to the original sender; bad signatures, expired
  tokens, wrong domains, non-null bounce senders, and SRS-looking catch-all
  addresses are rejected before DATA.
- A relay acceptance followed by a lost SMTP response does not silently erase
  the bounce record. `make test-email` makes its relay persist a message and
  drop the connection before the DATA acknowledgment, then stops the original
  Planner/Spinner pair and reverses that message's bounce through a fresh pair
  sharing the database and key. Also test independently deployed nodes with
  the production database and secret distribution.
- Live checks confirm MX delegation, SPF authorization of the outbound relay,
  and bounce return through the public SMTP listener.

The local unit and process-turnover SMTP smoke tests cover the basic two-way
return path, but
the remaining public-deployment gates above are not yet proven. The signed
forwarding-hop marker, source rate limit, and optional return token are only
partial safeguards, not a complete abuse or sender-authentication policy.
