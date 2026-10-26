# Delivery queue operations

Spinner can persist asynchronous HTTP and SMTP deliveries in its local spool.
Set `GATEWAY_DELIVERY_SPOOL_PATH` to a persistent, single-process directory.
The bundled Router launchers default it to
`GATEWAY_ROUTER_DATA_DIR/deliveries`.
On Linux and macOS, Spinner holds an exclusive OS lock on `spool.lock` for the
whole queue lifetime. A second process opening that path fails instead of
processing the same deliveries concurrently. Keep the lock file in place;
deleting or replacing it while Spinner runs defeats this fence. After an
abrupt owner exit, another process can reopen the same intact local spool and
resume pending deliveries. This uses a local filesystem lock, not a
cross-host lease or replicated queue.

To inspect and explicitly retry failures, enable a **separate private** Go
listener with `GATEWAY_DELIVERY_ADMIN_ADDR`. It requires the spool and
`GATEWAY_INTERNAL_TOKEN`. Do not publish this listener through the public
proxy or map its container port to the internet. Bind to loopback for a
colocated operator, or to an address reachable only from a trusted private
network when Planner is deployed separately. The public Router continues to
block `/_internal` traffic.

```sh
export GATEWAY_DELIVERY_SPOOL_PATH=/var/mod/gateway/deliveries
export GATEWAY_DELIVERY_ADMIN_ADDR=127.0.0.1:5052
export GATEWAY_DELIVERY_ADMIN_URL=http://127.0.0.1:5052
export GATEWAY_INTERNAL_TOKEN=YOUR_PRIVATE_INTERNAL_TOKEN
```

`GATEWAY_DELIVERY_ADMIN_URL` is set on Planner; it is the trusted URL Planner
uses to reach Spinner's admin listener. For separate containers, point it at
Spinner's private service name and keep the admin port unpublished. Planner
authenticates public management requests with `REGISTRY_TOKEN` and uses the
internal token only for the private Spinner hop.

For a fixed multi-node deployment, configure Planner with a JSON object of
stable node IDs and private admin origins instead of the single URL:

```sh
export GATEWAY_DELIVERY_ADMIN_NODES='{"north":"http://north.internal:5052","south":"http://south.internal:5052"}'
```

Each node must own a different persistent spool path and run the private admin
listener with the same internal token. Planner queries every configured node
for a list, merges the most recently updated statuses, and includes `node` in
each resource. It looks up an ID across nodes before sending a retry only to
the owning node. If a configured node is unavailable, listing fails with 503;
an absent delivery is reported as 404 only after every node was checked.
Keep the map current when adding or removing nodes. It is operator-owned
configuration, not automatic membership discovery; removing a node before its
spool is drained makes its deliveries inaccessible through Planner. URLs must
be trusted private HTTP(S) origins without embedded credentials or paths.

The bundled Router launchers default both admin addresses to loopback
port 5052. The FrankenPHP private listener exposes the generic Caddy HTTP
queue. Protocol Extensions own their delivery lifecycles behind the host
runtime contract instead of registering additional Caddy queues. The listener
is not mounted under the public Caddy route. Custom Caddyfiles must
configure `spool_path`, `admin_addr`, and Planner's
`GATEWAY_DELIVERY_ADMIN_URL` together. Caddy reloads that overlap an active
spool are rejected; restart the Router for spool configuration changes.

```text
GET  /deliveries?state=failed&limit=50
GET  /deliveries?event_id=INGRESS_EVENT_ID&limit=50
GET  /deliveries/{id}
POST /deliveries/{id}/retry
```

These public management routes return metadata only. If the private listener
is unavailable or misconfigured, Planner returns 503 rather than falling back
to direct spool access.

The admin listener accepts the shared internal token as a bearer credential:

```sh
curl -H "Authorization: Bearer $GATEWAY_INTERNAL_TOKEN" \
  'http://127.0.0.1:5052/deliveries?state=failed&limit=50'
curl -H "Authorization: Bearer $GATEWAY_INTERNAL_TOKEN" \
  'http://127.0.0.1:5052/deliveries/DELIVERY_ID'
curl -X POST -H "Authorization: Bearer $GATEWAY_INTERNAL_TOKEN" \
  'http://127.0.0.1:5052/deliveries/DELIVERY_ID/retry'
```

The default list state is `failed`; `queued`, `deferred`, and `delivered` are
also available. Lists return at most 100 records. Results contain metadata,
not payloads, headers, destination URLs, or transport credentials. A retry
only accepts a failed item and only if its adapter is still registered.
For an authenticated, Planner-approved asynchronous SMTP delivery,
`follow_up_id` identifies an Extension-produced delivery queued after terminal
failure. Email uses this generic hook for delivery-status notices.
That original item cannot be retried once the notice is queued; inspect the
notice as its own delivery instead.
Delivered receipts cannot be retried. Retry can duplicate a callback if its
destination accepted a previous attempt just before Spinner crashed: the
delivery guarantee is at-least-once, not exactly-once.
`event_id` instead lists all states for one ingress event, newest first; it
cannot be combined with `state`. Event IDs are included in status resources
when known. This filter scans the local spool directories, so use it for
troubleshooting, not high-frequency polling of a large spool. Old deliveries
created before event IDs were attached cannot be found through this filter.

The local spool belongs to one active Spinner process; replicas must not
simultaneously use one spool directory. The configured node map supports
inspection and redrive across a fixed fleet, but automatic membership, moving
ownership when a node is lost,
and distributed storage/replication are not implemented. Delivered receipts
are retained for 90 days by default and pruned on
startup and hourly. Set `GATEWAY_DELIVERY_RECEIPT_RETENTION` to another Go
duration or `0` to disable pruning in standalone and library/RoadRunner
runtimes. Pending and failed files are never pruned by this policy. The Caddy
host uses `receipt_retention` in its generic `gateway` block.

## Shared PostgreSQL queue (Spinner and all Router runtimes)

Apply Planner's `deliveries` migration to PostgreSQL, then give each Spinner
the same private database URL:

```sh
GATEWAY_DELIVERY_DATABASE_URL='postgres://gateway:...@db.internal/gateway?sslmode=verify-full'
```

Do not set `GATEWAY_DELIVERY_SPOOL_PATH` in this mode; the two settings are
mutually exclusive. Standalone Spinner, the FrankenPHP library and Caddy
Runtimes, and the RoadRunner Router select the shared queue. For a bundled
Router, set `GATEWAY_DELIVERY_DATABASE_URL` and use the same PostgreSQL URL
for Planner (`DB_URL`); the launcher chooses `DB_CONNECTION=pgsql` and applies
the Planner migrations there. The Caddy module's JSON or Caddyfile option is
`delivery_database_url` in the generic `gateway` block.
The shared queue supports every currently advertised delivery adapter,
idempotent submission receipts, metadata status/list/retry, delivered-receipt
pruning, and generic Extension-produced failure follow-ups. Email opts into
delivery-status notices with `webong/email`
`configuration.failure_notice_domain`; Spinner never constructs a DSN.
Because each Spinner reads the same delivery rows, one healthy private admin
listener can inspect the whole shared queue. Configure Planner with one
`GATEWAY_DELIVERY_ADMIN_URL`, or provide an ordered JSON list of two or more
private origins for Planner-side failover:

```sh
GATEWAY_DELIVERY_ADMIN_SHARED_URLS='["http://north.internal:5052","http://south.internal:5052"]'
```

Every listed origin must use the same migrated PostgreSQL queue and internal
token. Planner presents them as one logical `shared` node, retries read-only
requests and idempotent Email HTTP submissions on another origin after a
connection or 5xx failure, and probes for a live origin before sending a
delivery-redrive POST exactly once. A lost response to that POST is ambiguous:
Planner returns 503 rather than sending it again. The list is operator-owned.
Do not combine it with `GATEWAY_DELIVERY_ADMIN_NODES`, which is for independent
local spools. Alternatively, enable opt-in database-backed discovery on every
Planner and give each Spinner a unique, reachable private origin:

```sh
GATEWAY_DELIVERY_ADMIN_DISCOVERY_ENABLED=true
GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL=http://north.internal:5052
GATEWAY_DELIVERY_ADMIN_ADDR=0.0.0.0:5052
```

Use a different advertise origin on each node. It must point to that node's
private admin listener, not its public edge; keep port 5052 unpublished outside
the trusted network. Discovery requires the same migrated PostgreSQL database
and `GATEWAY_INTERNAL_TOKEN` on every replica. Spinner signs a short-lived
registration after binding its listener, renews it every five seconds, and
removes it on clean shutdown. A killed listener expires after about 20 seconds;
Spinner periodically prunes expired rows. Planner verifies signatures and
ignores expired rows; it fails closed if none are live. Do not set discovery alongside
`GATEWAY_DELIVERY_ADMIN_SHARED_URLS` or `GATEWAY_DELIVERY_ADMIN_NODES`. For a
separately hosted Planner, set only the discovery flag there; each Spinner
still needs the advertise URL. This does not make PostgreSQL highly available.
The generic Caddy queue retains its HTTP adapter for retry when it shares this
database. Protocol Extensions own any additional retry adapters through their
runtime contracts.
An original failed delivery and its deterministic notice are committed in one
PostgreSQL transaction, so process death cannot leave an unlinked notice.

PostgreSQL atomically claims due rows with a lease token. An expired lease can
be reclaimed; a stale worker cannot renew or settle after reassignment. Each
worker renews its lease while sending. This prevents duplicate *ownership* in
the database, not duplicate external effects when a worker loses its lease
after sending but before recording success; callbacks remain at-least-once.
Run `scripts/test-shared-delivery-store` with Docker and a locally available
`postgres:16-alpine` image to repeat the storage, two-worker queue, failure
notice, corrupt payload, and process-crash takeover checks.
With a built RoadRunner or FrankenPHP library Router image, run
`scripts/test-router-shared-postgres IMAGE static` and
`scripts/test-router-shared-postgres IMAGE discovery` to exercise the packaged Planner,
cross-replica subscription invalidation, persisted webhook delivery, and
private delivery-admin connection across two Router containers and a separate
PostgreSQL container. Static mode stops the first Router gracefully; discovery
mode kills it, waits for its registration to expire, and then verifies that the
surviving Planner can still inspect the shared delivery through another
private admin origin. This is one-host process failover, not cross-host
availability.
Payloads include message bodies and possibly delivery credentials: keep this
database private, access-controlled, encrypted where required, and backed up.
Cross-host failover tests remain open.
The process-crash test uses
two local processes and one disposable PostgreSQL container; it does not prove
host-level availability or exactly-once external delivery.
