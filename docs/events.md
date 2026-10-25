# Endpoint event history

Planner core owns the shared `events` ledger for endpoint-bound ingress
history. Protocol Extensions activate their recording and views without owning
or depending on one another's storage. DNS Zones, Endpoints, and Forwarders write query
events there. Create an enabled `POST /web/event-policy`
Planner resource to also record planned HTTP/webhook requests, WebSocket events,
and Webstream connects. Apply the Planner migrations before enabling it. This is
an ingress/planning record, **not** proof that an asynchronous subscriber
received a delivery; use `/deliveries` for delivery status.
Each event resource has a `deliveries_url` that filters durable delivery
receipts by its `event_id`. This can show queued, deferred, failed, and
delivered subscriber callbacks for that ingress event, but it cannot prove a
recipient processed the callback. A synchronous reply, a stream, or a volatile
worker handoff has no durable receipt. Delivered receipts may also expire
before event history does if the two retention periods are configured
differently.

Web history is off by default because it adds a database write per planned
event. When enabled, each row stores protocol, event kind, endpoint identity,
receipt ID, decision, request method/path, header **names**, source socket
address when supplied by Spinner, body byte count, and SHA-256 digest. It
does not retain header values, raw query values, or body bytes by default.
Spinner gives every HTTP ingress a distinct `ingress_id` while preserving the
stable delivery ID used for callback correlation and retries, so two identical
webhooks appear as two history entries.

An endpoint can opt into bounded body capture by supplying
`metadata._gateway.capture_body: true` when it is registered. Planner stores
at most `GATEWAY_EVENTS_MAX_BODY_BYTES` (default 65536, hard maximum 1048576)
as `payload.body_base64`, plus `payload.truncated`. Treat this as sensitive
data: the registry bearer token can read it, and the Planner database must be
protected accordingly. Only enable capture for endpoints whose owners and
retention requirements permit it. The body is not stored encrypted by this
feature. Header values and query values remain excluded even with capture.

```http
GET /registry/endpoints/{endpointKey}/events?protocol=http&kind=request&limit=50
Authorization: Bearer <REGISTRY_TOKEN>
```

The endpoint-history API uses the existing registry bearer token, accepts
`protocol`, `kind`, and `limit` filters, and returns cursor pagination. It
works for DNS endpoint events too; `/dns/endpoints/{endpoint}/events` remains the
DNS-specific view. Unknown endpoints return 404. The history route is a
management API and should not be exposed without the same access controls as
subscriptions.

`/web/event-policy` sets `retention_days` (default 30, range 1–3650) and can
be disabled without restarting Planner. On approximately
one in every thousand new event writes, Planner deletes older rows using the
indexed `received_at` column. This is traffic-triggered retention, not a
precise deletion deadline; installations with strict deletion SLAs should run
`php artisan gateway:prune-events` on a suitable schedule. DNS and Web history
share this retention policy. A history write failure is logged but never
changes the ingress route decision.
Disabling the Web event policy stops Web capture, not DNS history; its
retention value still governs existing DNS and Web event rows.
