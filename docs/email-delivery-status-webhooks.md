# Email delivery-status webhooks

Gateway can notify an Email account when an HTTP-submitted message reaches a
terminal Spinner delivery state. The account configures one callback in
Planner; delivery attempts and provider selection remain Gateway concerns.

## Configure the account hook

All hook-management requests require HTTPS and the account bearer token.

```http
POST /email/mailbox-accounts/{account}/delivery-status-hook
Authorization: Bearer {account-token}
Content-Type: application/json

{
  "callback_url": "https://app.example.com/gateway/email-status",
  "network_scope": "public",
  "webhook_secret": "64-lowercase-hex-characters",
  "enabled": true
}
```

The secret is write-only and encrypted separately from the Planner resource.
`GET` returns the configuration without the secret. `PATCH` accepts only
`{"enabled": false}` or `{"enabled": true}`. `DELETE` removes the hook.
Only one hook exists per account; change its destination or secret by deleting
and recreating it.

`network_scope` is required explicitly. A public callback must be an HTTPS
hostname on port 443. A `private` callback must exactly match an
operator-approved private callback grant in the Email receiving settings.

## Event body

Gateway emits `delivered` and `dead` terminal events. It may add `deferred` or
`failed` events in a compatible release; consumers must accept all four values.

```json
{
  "event": "email.delivery.status",
  "event_id": "d4c1...64 lowercase hex characters",
  "delivery_id": "7b4f...32 lowercase hex characters",
  "state": "delivered",
  "attempts": 2,
  "occurred_at": "2026-10-23T10:15:30Z",
  "reason_code": "accepted"
}
```

Current terminal `reason_code` values are:

- `accepted`: Spinner received a positive final delivery result.
- `acceptance_unknown`: the remote system may have accepted the message, but
  its final acknowledgement was lost. Do not automatically resend.
- `delivery_exhausted`: delivery reached a terminal rejection or exhausted its
  retry policy.

Provider response text and credentials are never included.

## Signature contract

Callbacks include:

```text
Content-Type: application/json
Idempotency-Key: gateway-email-delivery-{event_id}
X-Gateway-Hook-Id: {hook_id}
X-Gateway-Event-Id: {event_id}
X-Gateway-Signature: v1={lowercase hexadecimal HMAC-SHA256}
```

Decode the configured 64-character secret from hexadecimal to 32 bytes. Hash
the exact received request body with SHA-256, then build this UTF-8 canonical
string (every separator is one LF byte and there is no trailing LF):

```text
v1\n{hook_id}\n{event_id}\n{lowercase_hex_sha256_of_exact_body}
```

The signature is HMAC-SHA256 of that canonical string using the decoded secret.
Compare signatures in constant time before parsing or acting on the event.

Gateway retries non-2xx responses and connection failures with exponential
backoff. Retries preserve the exact body, event ID, idempotency key, and
signature. A crash after the receiver accepted a request can still cause a
duplicate, so consumers must deduplicate on `event_id`.

Planner durably records the receipt watch when it accepts the original email
submission. The bundled Spinner scheduler triggers observation and callback
dispatch; installations without that polling loop can run:

```bash
php artisan gateway:send-email-delivery-status-events
```

`GET /email/submissions/{delivery_id}` and the operator
`GET /deliveries/{delivery_id}` APIs remain polling fallbacks.

To retry a terminal email, submit the message again with a new
`Idempotency-Key`. Gateway returns a new delivery ID. Reusing the old key replays
the old receipt and never creates a new attempt.
