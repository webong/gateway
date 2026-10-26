# Kubernetes deployments

Gateway ships four core Kubernetes configurations, plus optional Spinner
Email, DNS, and TLS edge overlays:

| Configuration | Container | Planner | Persistent state |
| --- | --- | --- | --- |
| [`infra/kubernetes/router`](../infra/kubernetes/router) | Combined Router image from `infra/docker/Gateway.router.Dockerfile`; select RoadRunner (default) or FrankenPHP with `GATEWAY_ROUTER_RUNTIME` | Bundled Planner, hosted by RoadRunner workers or the FrankenPHP Go library | `router-data` PVC for SQLite, keys, cache, and storage |
| [`infra/kubernetes/spinner`](../infra/kubernetes/spinner) | Standalone image from `infra/docker/Gateway.spinner.Dockerfile` | Separate Laravel service at `GATEWAY_PLANNER_URL` | None for the default HTTP transport |
| [`infra/kubernetes/planner`](../infra/kubernetes/planner) | Standalone PHP-FPM/Laravel Octane image from `infra/docker/Gateway.planner.Dockerfile`; select PHP-FPM, FrankenPHP, Swoole, or RoadRunner with `GATEWAY_PLANNER_SERVER` | Runs the Planner and registration API | External PostgreSQL; ephemeral local cache/storage |
| [`infra/kubernetes/provision`](../infra/kubernetes/provision) | Dedicated target worker from the `kubernetes` target of `infra/docker/Gateway.provision.Dockerfile` | Separate private Planner connection | Planner target state; no local worker database |

Build the image you need, publish it to a registry reachable by your cluster,
and replace the example `image:` value in that configuration before applying
it. The Router and Spinner manifests do not publish a public listener. Attach
your cluster's ingress/controller to the `router` or `spinner` Service, or
deploy the optional [TLS edge](../infra/kubernetes/tls-edge) below.

The combined Router Dockerfile supports both runtimes. Select RoadRunner
(default) or FrankenPHP at build time, then use the resulting image on the same
Router Deployment. Both variants use port 8080, the `/health` probe, and the
`/data` PVC. Keep the single-replica SQLite deployment strategy with either
runtime.

## Combined Router

```sh
# RoadRunner (default)
docker build -f infra/docker/Gateway.router.Dockerfile -t REGISTRY/router:TAG .
# Or select the FrankenPHP Go-library runtime:
docker build --build-arg GATEWAY_ROUTER_RUNTIME=frankenphp \
  -f infra/docker/Gateway.router.Dockerfile -t REGISTRY/router:TAG .
docker push REGISTRY/router:TAG
kubectl create namespace gateway
# Set image: REGISTRY/router:TAG in infra/kubernetes/router/deployment.yaml.
kubectl -n gateway apply -k infra/kubernetes/router
kubectl -n gateway rollout status deployment/router
```

The `router` Service exposes port 80 to the single Router Pod's port 8080.
Startup generates credentials under `/data`, applies migrations, and serves
`/health`. Its readiness probe uses `/ready` to query the migrated endpoint
table; liveness continues to use `/health`. The Deployment uses `Recreate` with
one replica so an update cannot
run two SQLite writers against the same `router-data` claim. Keep and back up
the PVC across image upgrades; losing `gateway.env` while retaining the database
prevents startup. Do not scale this SQLite configuration beyond one replica.

## Standalone Planner and Spinner

The standalone Planner gives the standalone Spinner a private HTTP backend. Both
are parts of Gateway: Spinner serves public protocol traffic, while Planner
handles registration and desired state. The Planner image serves Laravel on
port 8080; it does not include the Go router. Run its migration Job against a
backed-up PostgreSQL database before starting or upgrading Planner. The Job is
intentionally separate from the Deployment so replicas never race migrations.

Create three Secrets/ConfigMaps in the Gateway namespace:

- `planner-credentials`: `app-key` (a stable Laravel `base64:` key),
  `database-url` (a PostgreSQL URL), and `registry-token` (a strong token).
- `spinner-planner`: `internal-token` (the same strong token used by both
  services). Keep this token private; it authenticates Spinner/Planner calls.
- `planner-settings`: `public-url` (the externally reachable HTTPS Gateway
  origin used in generated registrations).

Build and publish both images, replace their example `image:` references, and
apply the migration Job once per version before the services:

```sh
docker build -f infra/docker/Gateway.planner.Dockerfile -t REGISTRY/planner:TAG .
docker build -f infra/docker/Gateway.spinner.Dockerfile -t REGISTRY/spinner:TAG .
docker push REGISTRY/planner:TAG
docker push REGISTRY/spinner:TAG
kubectl create namespace gateway
# Create planner-credentials, spinner-planner, and planner-settings securely.
# Set both published image tags in the manifests.
kubectl -n gateway apply -f infra/kubernetes/planner-migrate/job.yaml
kubectl -n gateway wait --for=condition=complete job/planner-migrate --timeout=180s
kubectl -n gateway apply -k infra/kubernetes/planner
kubectl -n gateway apply -k infra/kubernetes/spinner
kubectl -n gateway rollout status deployment/planner
kubectl -n gateway rollout status deployment/spinner
```

For the next upgrade, remove the completed Job before creating it with the new
image tag. Its Pod needs network access to PostgreSQL; the Planner and Spinner
Pods need private reachability to each other. The `planner` Service is
ClusterIP at `http://planner:8080`, matching Spinner's default
`GATEWAY_PLANNER_URL`. Do not publish this Service wholesale: it also
contains internal routes. External clients use the public Spinner gateway
origin for authenticated Planner management routes such as
`/registry/endpoints`. The Planner kustomization includes an ingress
NetworkPolicy allowing port 8080 only from `spinner`, `provision-worker`, and
Planner Pods in the same namespace. Use a CNI that enforces NetworkPolicy;
ClusterIP and a policy object alone are not proof of isolation. Add other
Planner callers explicitly if your installation needs them. Configure public
ingress TLS and namespace/egress policy for your cluster.

To exercise the Planner, Spinner, and TLS-edge images, migration Job,
PostgreSQL connection, private Planner Service, and authenticated public
management proxy in a disposable OrbStack namespace:

```sh
docker build -f infra/docker/Gateway.planner.Dockerfile -t gateway-planner:local .
docker build -f infra/docker/Gateway.spinner.Dockerfile -t gateway-spinner:local .
docker build -f infra/docker/Gateway.tls-edge.Dockerfile -t gateway-tls-edge:local .
bash scripts/test-kubernetes-planner
GATEWAY_DNS_EXTENSION_URL=https://extensions.example.test/webong-dns.tar.gz \
GATEWAY_DNS_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_DNS_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-planner --with-dns
docker build -f infra/docker/email-relay-fixture.Dockerfile -t gateway-email-relay-smoke:local .
GATEWAY_DNS_EXTENSION_URL=https://extensions.example.test/webong-dns.tar.gz \
GATEWAY_DNS_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_DNS_EXTENSION_VERSION=1.0.0 \
GATEWAY_EMAIL_EXTENSION_URL=https://extensions.example.test/webong-email.tar.gz \
GATEWAY_EMAIL_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_EMAIL_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-planner --with-email
GATEWAY_DNS_EXTENSION_URL=https://extensions.example.test/webong-dns.tar.gz \
GATEWAY_DNS_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_DNS_EXTENSION_VERSION=1.0.0 \
GATEWAY_EMAIL_EXTENSION_URL=https://extensions.example.test/webong-email.tar.gz \
GATEWAY_EMAIL_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_EMAIL_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-planner --with-email --with-dns
GATEWAY_WEB_EXTENSION_URL=https://extensions.example.test/webong-web.tar.gz \
GATEWAY_WEB_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_WEB_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-planner --with-webstream
```

The smoke registers an endpoint with the bundled Planner's configured
`gateway` client, rejects a missing registry token, and verifies that Spinner
does not expose `/_internal` routes. It repeats those checks through a trusted
self-signed HTTPS host-to-NodePort edge, checks that an unrelated PostgreSQL
Pod cannot reach the Planner Service under the ingress policy, replaces the
Planner Pod, and confirms that the registered endpoint remains readable
through the new Pod. It also scales Planner to two Pods and reads that endpoint
directly from each, then registers one managed endpoint simultaneously from
both Pods and checks that PostgreSQL holds one record. This checks
PostgreSQL-backed endpoint persistence through Planner process loss, shared
endpoint reads, and this registration race across replicas. At the end it stops
the disposable PostgreSQL Pod and verifies that Planner `/ready` returns 503
while `/health` remains 204, and Spinner `/ready` returns 503 while its
`/health` remains 200. It does not prove
every Planner operation safe under concurrent replicas or establish PostgreSQL
failover, public DNS, internet reachability, certificate issuance/renewal, or
production TLS policy.

The optional `--with-email` mode uses a local recording relay. It marks its
disposable `.test` domain verified directly in temporary PostgreSQL state so
it can exercise managed SMTP routing; it does **not** prove live TXT ownership.
It checks STARTTLS, recipient decisions, relay acceptance, and the private
delivery-admin connection, not public MX reachability or deliverability.
The optional `--with-webstream` mode registers a private in-cluster SSE
source through the production Planner and verifies that the production
Spinner rejects it before contacting the source. It then grants that exact
source origin and updates the active `webong/web` Extension configuration with
the Service IP ceiling, then verifies the SSE event arrives without restarting
the generic Spinner Pod. This checks the default
private-address protection and an explicitly authorized in-cluster source;
it does not establish successful streaming from a publicly reachable source
or general permission to connect to other private application services.

The manifest's `/data`, `/config`, and `/tmp` mounts are disposable. All
durable Planner state lives in PostgreSQL; back it up and keep `APP_KEY` stable
across rollouts. Additional filesystem-backed extension data requires its own
persistent storage or an external store. Planner's `/health` liveness probe
checks the Laravel HTTP process; `/ready` checks that the database and migrated
endpoint table can be queried before Kubernetes sends traffic to the Pod. It
does not check Spinner or every Planner dependency.

Spinner's HTTP-mode `/ready` makes a bounded, token-authenticated request over
its configured private Planner transport to
`/_internal/gateway/ready`, which the Gateway Planner package installs in
any Laravel host. Its Kubernetes readiness probe
uses this endpoint, so an unavailable Planner removes Spinner from the HTTP
Service endpoints without restarting the Go process. Spinner `/health` remains
a process-only liveness check. Other protocol listeners can still be running
while Spinner is HTTP-unready; configure their exposure and health separately.

## Spinner alone

```sh
docker build -f infra/docker/Gateway.spinner.Dockerfile -t REGISTRY/spinner:TAG .
docker push REGISTRY/spinner:TAG
kubectl create namespace gateway
# Set image: REGISTRY/spinner:TAG and your Planner URL in
# infra/kubernetes/spinner/deployment.yaml.
kubectl -n gateway create secret generic spinner-planner \
  --from-file=internal-token=/secure/path/to/planner-token
kubectl -n gateway apply -k infra/kubernetes/spinner
kubectl -n gateway rollout status deployment/spinner
```

The `spinner` Service exposes port 80 to the Go Pod's port 5001. The token in
the Secret must match the external Planner's `GATEWAY_INTERNAL_TOKEN`; the
Planner URL must be a private, reachable address. This configuration does not
install Planner or its database. If you enable Spinner features with durable
local state, such as the outbound SMTP spool, add an appropriate volume and
configuration before deploying them. Its `/health` endpoint reports Spinner
health, not the external Planner's readiness. The image declares numeric
UID/GID 10001 so the Deployment's `runAsNonRoot` policy can verify it.

## Email SMTP overlay

`infra/kubernetes/spinner-email` adds an opt-in SMTP listener to the Go-only
Spinner configuration. It binds container port 2525 and creates a separate
`spinner-smtp` ClusterIP Service on TCP port 25. The overlay requires a
`gateway-smtp` ConfigMap with `hostname` and a `gateway-smtp-tls` TLS Secret;
the private key is mounted read-only for the unprivileged Spinner. It does not
publish SMTP outside the cluster or enable outbound relay by itself. Recipient
preflight is explicitly enabled, so Planner rejects unknown aliases during
`RCPT TO` even when an outbound relay has not been configured.

```sh
kubectl -n gateway create configmap gateway-smtp \
  --from-literal=hostname=mx1.example.com
kubectl -n gateway create secret tls gateway-smtp-tls \
  --cert=/secure/path/fullchain.pem --key=/secure/path/privkey.pem
# Set the published Spinner image and private Planner URL in
# infra/kubernetes/spinner/deployment.yaml first.
kubectl -n gateway apply -k infra/kubernetes/spinner-email
kubectl -n gateway rollout status deployment/spinner
```

For public Email, separately publish TCP port 25, configure MX records, and
enable an outbound relay and durable queue. Multiple Spinner replicas must use
the shared PostgreSQL delivery queue or independent persistent spools; do not
share a local spool directory among Pods. Complete the [Email authentication
gate](email-authentication.md) and verify the public MX/relay/bounce path before
accepting internet mail. This overlay only packages the inbound listener.

A disposable OrbStack smoke builds the production Spinner image and Planner
test fixture, creates a managed alias, verifies STARTTLS and accepted/rejected
recipient decisions through a temporary NodePort, then repeats after one of
two Spinner Pods is deleted:

```sh
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-email-spinner-smoke:local .
docker build -f infra/docker/planner-fixture.Dockerfile \
  -t gateway-k8s-planner-smoke:local .
bash scripts/test-kubernetes-email
```

The smoke deletes its temporary namespace and uses a one-day self-signed test
certificate. It does not test outbound delivery or public Email operations.

### Outbound relay and shared asynchronous queue

`infra/kubernetes/spinner-email-relay` extends the Email smoke topology with a
shared PostgreSQL queue for asynchronous deliveries and a private
`spinner-delivery-admin` ClusterIP Service on port 5052. Apply Planner's
`deliveries` migration to the same PostgreSQL database before starting
Spinner. Configure Planner's `GATEWAY_DELIVERY_ADMIN_URL` as
`http://spinner-delivery-admin:5052` and give it the same
`GATEWAY_INTERNAL_TOKEN`; never publish this admin Service. Every Spinner
replica uses the same database URL. For Email and DNS together with forwarding,
use `infra/kubernetes/spinner-email-dns-relay` instead. The standalone Planner
counterparts are `infra/kubernetes/planner-email-relay` and
`infra/kubernetes/planner-email-dns-relay`; they set the private admin URL and,
for the combined overlay, read the shared DNS zone ConfigMap.

```sh
kubectl -n gateway create secret generic gateway-shared-delivery \
  --from-literal=database-url='postgres://USER:PASSWORD@db.internal/gateway?sslmode=verify-full'
# After the Planner database migrations and base Email configuration:
kubectl -n gateway apply -k infra/kubernetes/planner-email-relay
kubectl -n gateway apply -k infra/kubernetes/spinner-email-relay
kubectl -n gateway rollout status deployment/planner
kubectl -n gateway rollout status deployment/spinner
```

For Email and DNS together, apply `infra/kubernetes/planner-email-dns-relay`
and `infra/kubernetes/spinner-email-dns-relay` in place of the two Email-only
overlays. The Planner overlay enables API-managed DNS zones, and the Spinner
relay still uses the shared PostgreSQL queue. Configure the relay itself on the
`webong/email` `/extensions` resource:

```json
{"smtp_relay":{"address":"relay.internal:587","server_name":"relay.internal","tls_mode":"starttls"}}
```

Relay credentials do not belong in a generic Spinner Deployment or non-secret
Extension configuration. Put `credential_ref` in `smtp_relay`, then write the
referenced `{"username":"...","password":"..."}` or OAuth-token object to
`/extensions/{activation-id}/secrets/{reference}` over HTTPS. Spinner resolves
it through the authenticated private Planner connection for each attempt. The
database URL remains a deployment secret. Use `starttls` or `implicit` in production.
Do not substitute `sslmode=disable` for a remote production database. The
overlay does not deploy PostgreSQL or the relay; operate them with persistent
storage, backups, TLS, and appropriate network policy. See [Delivery queue
operations](deliveries.md#shared-postgresql-queue-spinner-and-all-router-runtimes)
for retry and admin behavior.

Managed inbound SMTP forwarding uses a synchronous `SUBMIT` decision: Spinner
acknowledges DATA only after the outbound relay accepts the message. It does
not create a local delivery receipt for that handoff. Authenticated Email HTTP
submissions and other asynchronous deliveries do enter the shared PostgreSQL
queue and retain status receipts. Both flows require the configured relay.

A disposable OrbStack test can exercise the full local path with PostgreSQL
and a recording SMTP relay fixture:

```sh
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-email-spinner-smoke:local .
docker build -f infra/docker/planner-fixture.Dockerfile \
  -t gateway-k8s-planner-smoke:local .
docker build -f infra/docker/email-relay-fixture.Dockerfile \
  -t gateway-email-relay-smoke:local .
bash scripts/test-kubernetes-email --with-relay
bash scripts/test-kubernetes-email --with-dns --with-relay
```

The fixture uses one disposable PostgreSQL Pod and an unencrypted in-cluster
test relay. It checks direct SMTP handoff separately from queued Email HTTP
submission, delivered receipt, and idempotent replay after a Spinner Pod
replacement. It does not prove database durability after database loss,
external relay policy, or public deliverability.

## Authoritative DNS overlay

`infra/kubernetes/spinner-dns` publishes the port owned by the installable DNS
Extension. It opens UDP and TCP port 5353 in Spinner
and provides a separate `spinner-dns` Service on port 53 for both protocols.
The Service is ClusterIP by default; applying the overlay does not expose DNS
outside the cluster. The overlay only declares and publishes ports; it does not
configure or bundle the Extension. Install and enable `webong/dns` through
Planner's `/extensions` API with `configuration.address` set to `:5353`, then
create zones through `POST /dns/zones`. Planner projects their active desired
state into the Extension snapshot. Configure the matching internal token and
set the published Planner and Spinner image tags before applying the overlays.

```sh
kubectl -n gateway apply -k infra/kubernetes/planner-dns
kubectl -n gateway apply -k infra/kubernetes/spinner-dns
kubectl -n gateway rollout status deployment/planner
kubectl -n gateway rollout status deployment/spinner
# Activate webong/dns, then call POST /dns/zones for dns.example.com.
```

The standalone production-image smoke above creates an Endpoint through Spinner
and queries its authoritative name through temporary UDP and TCP NodePorts.
The separate `scripts/test-kubernetes-dns` fixture still covers two zones and
Spinner Pod replacement. Neither local smoke proves public delegation or
distinct host-level nameserver availability.

To serve a public delegated zone, publish `spinner-dns` on port 53 over both
UDP and TCP using an appropriate load balancer or host mapping. Configure the
parent-zone delegation and nameserver addresses separately. Place at least two
Spinner replicas in distinct failure domains and provide distinct reachable
nameserver endpoints for actual DNS availability; two Pods on one OrbStack node
only provide process resilience. See [Authoritative DNS across two Spinner
nodes](dns-ha.md) for delegation and direct-query checks.

A disposable OrbStack smoke builds the production Spinner image and a Planner
test fixture, creates two zones through Planner, checks authoritative UDP and
TCP answers through temporary NodePorts, and rechecks after a Spinner Pod is
deleted:

```sh
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-dns-spinner-smoke:local .
docker build -f infra/docker/planner-fixture.Dockerfile \
  -t gateway-k8s-planner-smoke:local .
GATEWAY_DNS_EXTENSION_URL=https://extensions.example.test/webong-dns.tar.gz \
GATEWAY_DNS_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_DNS_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-dns
```

The DNS variants require the same three `GATEWAY_DNS_EXTENSION_*` variables
shown above. The archive must be the matching-platform `webong/dns` package,
served over HTTPS with a certificate trusted by Spinner. The smoke activates it
through Planner before creating Zones and Endpoints; the image does not bundle
DNS or accept listener environment variables.

The smoke deletes its temporary namespace. It does not configure public
delegation, TLS, or cross-host availability.

To enable Email and DNS on the same Spinner Deployment, apply
`infra/kubernetes/spinner-email-dns` instead of applying the two individual
overlays in sequence. It composes their Services and listener settings with
the base Web configuration. Create the SMTP ConfigMap and TLS Secret first;
DNS zones come from Planner's API. Reapplying an individual overlay afterward would remove the other
overlay's listener settings from the Deployment. The combined overlay's
OrbStack smoke checks SMTP STARTTLS and managed-recipient decisions alongside
authoritative DNS over UDP and TCP, then repeats them after one Pod is deleted:

```sh
# Build the two smoke images shown in the Email section above first.
GATEWAY_DNS_EXTENSION_URL=https://extensions.example.test/webong-dns.tar.gz \
GATEWAY_DNS_EXTENSION_SHA256=<archive-sha256> \
GATEWAY_DNS_EXTENSION_VERSION=1.0.0 \
  bash scripts/test-kubernetes-email --with-dns
```

This still uses one node, a self-signed test certificate, and temporary
NodePorts. It does not prove public SMTP or DNS reachability, delivery, or
multi-host resilience.

## Optional TLS edge

`infra/kubernetes/tls-edge` runs Caddy as a separate non-root TLS terminator
in front of either the `spinner` or `router` Service. Supply a certificate
and key for your public hostname in the `gateway-tls` Secret, then set
`GATEWAY_PUBLIC_HOST` and `GATEWAY_TLS_UPSTREAM` in its Deployment. The example
upstream is `http://spinner:80`; use `http://router:80` for the combined Router.
The edge Service is ClusterIP by default so applying the example cannot
publish a listener accidentally.

```sh
docker build -f infra/docker/Gateway.tls-edge.Dockerfile \
  -t REGISTRY/tls-edge:TAG .
docker push REGISTRY/tls-edge:TAG
kubectl -n gateway create secret tls gateway-tls \
  --cert=/secure/path/fullchain.pem --key=/secure/path/privkey.pem
# Set the published image, hostname and upstream in
# infra/kubernetes/tls-edge/deployment.yaml.
kubectl -n gateway apply -k infra/kubernetes/tls-edge
kubectl -n gateway rollout status deployment/gateway-tls-edge
```

Publish the `gateway-tls-edge` Service on port 443 using a LoadBalancer,
NodePort with an external load balancer, or another cluster-specific method.
Restrict the direct Router/Spinner Service to the private cluster network.
This example consumes an operator-provided certificate; it does not request,
renew, or rotate certificates. Keep `gateway-tls` available during upgrades
and rotate it before expiry. A disposable OrbStack smoke in
`scripts/test-kubernetes-provisioning` checks a temporary self-signed test
certificate, trusted host-to-NodePort WebSocket upgrade, and private Planner
route isolation before and after worker recovery. That does not establish
public DNS, internet reachability, certificate issuance, or production TLS
policy.

## Provisioning worker

The dedicated worker creates and watches managed Reverb, Mercure, or
Centrifugo Pods; it is not a public Spinner listener. Build its image with
`--target kubernetes`, create a Kubernetes target in Planner, supply the
one-time target ID/token through the `provision-worker-target` Secret, and
replace the example image and Planner URL in
[`infra/kubernetes/provision`](../infra/kubernetes/provision). The target must
use context `gateway-in-cluster`, kubeconfig path
`/tmp/gateway-in-cluster.kubeconfig`, and the manifest's namespace. The worker
adapts its mounted ServiceAccount token into that explicit kubeconfig path.
See [Deployment targets](provisioning-targets.md) for the target body, RBAC,
security limits, and verification status.
