# Approved Gateway HTTP delivery v1

The deployed catalog key `gateway.http.v1` sends resolved step input as a JSON
object through Gateway's existing synchronous HTTP delivery capability. The
workflow work notification still arrives through Gateway's durable queue and the
atomic execution gate. The extension does not open an outbound provider socket
from PHP or introduce another delivery queue.

Operators publish a destination alias and `destination_sha256` in an immutable
step definition. Activation's `http_destinations` maps that alias to an
identity-scoped secret reference. Clients can supply declared input/context and
logical IDs, but cannot select URLs, headers, commands, secrets or classes.

A private destination secret has this shape (these are example values):

```json
{
  "url": "https://provider.example/fulfil",
  "effect_scope": "provider-account-1",
  "method": "POST",
  "headers": {"Authorization": ["Bearer <provider credential>"]},
  "idempotent": true,
  "idempotency_header": "Idempotency-Key",
  "timeout_seconds": 10
}
```

`effect_scope` is the stable provider account and effect-deduplication domain.
Verify it against the provider account when provisioning or rotating credentials.
It must change if the credential selects another account, merchant, tenant or
idempotency namespace. A credential rotation is safe for automatic replay only
when that independently verified scope and provider guarantee remain unchanged.
No generic HTTP client can infer this account identity from an opaque bearer token.

The pin is SHA-256 over method, URL, effect scope and lower-case receipt-header
name, separated by NUL bytes. Compute it without displaying the endpoint or token:

```sh
GATEWAY_WEAVER_HTTP_DESTINATION=/private/orders-provider.json php bin/destination-fingerprint
```

The file must be mode 0600. Store its secret through the existing scoped secret
management route and put only the resulting digest/ref/alias in desired resources.
Changing URL, method, effect scope or receipt-header contract requires a new
explicit versioned destination/definition and reconciliation of uncertain effects.
Readiness and execution reject mismatches before sending another request.

Gateway retains its production egress-address policy. Private, loopback,
link-local and special-use provider addresses are not enabled by this operation.
Use operator-reviewed HTTP(S) destinations accepted by that policy; HTTPS uses
Gateway's ordinary certificate verification. The Go supervisor holds provider
URLs/credentials; child context contains only a private loopback bridge credential
and non-secret binding fingerprints/replay guarantees. Secret loss, rotation or
version changes stop the runtime before it admits more claims.

## Replay and failures

A provider-side idempotency guarantee and the step's explicit idempotent retry
policy are both required for automatic uncertain replay. The receipt key comes
from the queue's stable run/step effect identity, never client context. Attempts
reuse that key. The provider must implement atomic receipt/effect deduplication;
setting `idempotent: true` is an operator assertion of that external contract,
not an exactly-once guarantee supplied by Weaver.

Without a provider replay guarantee, the default correlation header is
`X-Weaver-Effect-Key`; `Idempotency-Key`/`X-Idempotency-Key` are rejected so the
HTTP transport is not falsely told that a non-idempotent request is replayable.
HTTP 2xx completes with output `{"http_status":204}` (the actual status). HTTP
5xx/408/429 and transport uncertainty retry only under both guarantees. Other
rejections fail the attempt. Provider headers/body and transport errors do not
enter public state; uncertain non-idempotent effects require reconciliation.

Provider requests are bounded to 64 KiB and 1–20 seconds. The worker atomically
extends a still-valid claim's execution budget to at least 30 seconds before
running effects, without shortening longer leases or reviving expired claims.
Gateway delivery and workflow execution leases remain independent.

See `examples/approval-http-order.configuration.json`: start the order with
`context.order_id`, complete its approval through the authenticated callback,
and let the dependent HTTP step fulfil it. The example pin matches the example
URL, scope and default idempotency header above. Replace all operator bindings and
credentials before deployment.
