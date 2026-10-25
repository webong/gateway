# Deployment targets: first milestone

Gateway can register an existing remote Docker host or Kubernetes cluster as a
target. Planner owns target records and placement; a Go provisioning worker
executes the existing Reverb, Mercure, and Centrifugo workloads there. This does
not create cloud VMs or install Docker/Kubernetes on a bare server.

The design keeps the target connection in the provisioning
module, independent of technology integrations. Integrations still import the
public Arranger extension contracts, not Spinner implementation code.

## Register a target

Apply Planner migrations first (`php artisan migrate` in the host application).
Back up existing production data before deployment. The migration adds the
configurable `targets` table and nullable `target_id` columns to servers/nodes;
existing servers remain in the legacy, untargeted pool.

These management routes use the existing registry bearer authentication:

```text
POST   /targets
GET    /targets
GET    /targets/{target}
DELETE /targets/{target}
POST   /targets/{target}/rotate-token
```

Example body for `POST /targets`:

```json
{
  "name": "production-docker",
  "driver": "docker",
  "connection": {
    "context": "production-ssh",
    "publish_host": "10.0.0.10",
    "route_host": "10.0.0.10"
  }
}
```

The named Docker context must already exist **on the worker**, including SSH or
TLS access. `publish_host` is the bind address on the remote Docker host;
`route_host` is the IP reachable from the worker. Context selection is explicit
on every command, never a global `docker context use` change. See the official
[Docker context documentation](https://docs.docker.com/engine/manage-resources/contexts/).

Kubernetes example:

```json
{
  "name": "production-cluster",
  "driver": "kubernetes",
  "connection": {
    "context": "production",
    "kubeconfig": "/run/secrets/production.kubeconfig",
    "namespace": "realtime",
    "route_mode": "pod-ip"
  }
}
```

The absolute kubeconfig path refers to a trusted file on the worker. Each CLI
invocation pins that file, context, and namespace, including port-forwarding,
monitoring, and deletion. Namespace creation is an operator responsibility.
`pod-ip` requires network reachability from worker to Pods. `port-forward` creates
worker-local tunnels; their loopback addresses are not remotely reachable.

Local targets use `{"name":"local-host","driver":"local"}` and execute on the
worker host. Use one worker for a local target so its meaning stays unambiguous.

Creation returns `id` and a one-time `worker_token`. Store the token securely;
listing/showing targets never returns it. Planner stores only its SHA-256 hash.
Connection references are encrypted at rest. Raw SSH keys, kubeconfig contents,
and arbitrary connection properties are rejected by the management interface.

Target connections and a server's target are immutable in this milestone.
Create a new target/server for relocation. Do not repoint a context to another
daemon/cluster while its worker is running. Deleting a target requires removing
its servers and stopping workers; expired node records are removed with it.

## Build and run a worker

From the repository root:

```sh
GOWORK=off go -C pkg/spinner build -mod=readonly -o /tmp/gateway-provision ./cmd/provision

export GATEWAY_PLANNER_URL=https://planner.example.com
export GATEWAY_PROVISIONING_TARGET_ID=TARGET_ID_FROM_RESPONSE
export GATEWAY_PROVISIONING_TARGET_TOKEN=WORKER_TOKEN_FROM_RESPONSE
export GATEWAY_NODE_ID=production-worker-1

/tmp/gateway-provision --check
```

Use your secret manager rather than saving a real token in shell history.
Node IDs must be unique across targets and stable across restarts. Only run one
process with a given node ID. The dedicated worker runs explicitly and does not
need `GATEWAY_PROVISIONING_ENABLED` or a shared `GATEWAY_INTERNAL_TOKEN`.

`--check` fetches the authenticated target and makes read-only remote checks. It
does not register a worker, reset instance reports, create Pods/containers, or
run startup cleanup. Docker checks daemon connectivity with `info`; Kubernetes
checks permissions using [kubectl auth can-i](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_auth/kubectl_auth_can-i/).
Local targets only verify Planner access at this stage. These checks do not prove
image availability, admission-policy acceptance, or data-plane reachability;
workload startup checks those separately.

Build or install an executable integration and supply its workload settings, for example:

```sh
go -C pkg/centrifugo build -mod=readonly -o /tmp/gateway-centrifugo ./cmd/integration
export GATEWAY_INTEGRATION_PATHS=/tmp/gateway-centrifugo
export GATEWAY_CENTRIFUGO_DOCKER_IMAGE=YOUR_PINNED_CENTRIFUGO_IMAGE
/tmp/gateway-provision
```

For Kubernetes use `GATEWAY_CENTRIFUGO_KUBERNETES_IMAGE` instead. See each
integration's README for image requirements and launch settings. The dedicated
worker opens no inbound network listener and does not embed PHP or QuickJS.
Credentials need only be installed on the worker, not on the public router.

### Docker-target worker image

`infra/docker/Gateway.provision.Dockerfile` packages the dedicated worker,
the three bundled executable workload integrations, the Docker client, and SSH
client support. Build it from the repository root:

```sh
docker build -f infra/docker/Gateway.provision.Dockerfile -t gateway-provision:local .
scripts/test-provision-worker-image gateway-provision:local
GATEWAY_TEST_PROVISION_WORKER_IMAGE=gateway-provision:local \
  GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestProvisionWorkerImageChecksRealPlanner$' -count=1
GATEWAY_TEST_PROVISION_WORKER_IMAGE=gateway-provision:local \
  GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestProvisionWorkerImageRecoversCentrifugo$' -count=1
```

Give the container a private route to Planner and a stable `GATEWAY_NODE_ID`.
The image defaults `GATEWAY_INTEGRATION_PATHS` to the three bundled executables;
set it explicitly to a colon-separated list of absolute paths if only a subset
is installed. Configure the matching `GATEWAY_*_DOCKER_IMAGE` values for the
server types this worker may run. The image contains no Docker daemon and no
Laravel application image. Workload images must be available to the target
daemon separately.

For a **host-network-capable, local Docker-socket** check, use a private environment file
containing the target ID, one-time worker token, Planner URL, and stable node
ID. Keep it out of source control and limit read access:

```sh
docker run --rm --network host --user 0:0 \
  --env-file ./worker.env \
  --mount type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock \
  gateway-provision:local --check
```

Remove `--check` to run reconciliation after the read-only check succeeds.
`--network host` lets the worker reach workload ports published to the Docker
host loopback; other networking arrangements must set a reachable
`GATEWAY_PROVISIONING_DOCKER_ROUTE_HOST` and an appropriate publish address.
The first opt-in integration test above exercised this shape with OrbStack and
a disposable real Planner target; it checks target access and daemon
connectivity without registering a node. The second starts a real Centrifugo
workload from the packaged worker, hard-kills that worker, and verifies that a
replacement adopts the same healthy container and PID. Both use one local
Docker daemon; neither proves remote-host or live-Kubernetes recovery.
Access to the Docker socket is effectively host-administrator access; the
example runs as root only to match typical socket permissions. Prefer a
remote TLS/SSH Docker context with scoped credentials where possible. The
image's default user is unprivileged, and a remote context can be mounted
under that user's home directory. Do not expose the Docker socket or worker
token through the public Gateway edge.

### Kubernetes-target worker image

Build the `kubernetes` target of the same Dockerfile and run its no-cluster
packaging smoke:

```sh
docker build -f infra/docker/Gateway.provision.Dockerfile --target kubernetes \
  -t REGISTRY/gateway-provision-kubernetes:TAG .
scripts/test-provision-worker-image REGISTRY/gateway-provision-kubernetes:TAG kubernetes
```

The image includes `kubectl` and the three executable workload integrations,
but not Docker or a public listener. Its startup adapter writes
`/tmp/gateway-in-cluster.kubeconfig` from the Pod's mounted ServiceAccount
token **path** and CA path; it does not copy the token into that file. The
target record must pin that exact path and context:

```json
{
  "name": "gateway-cluster",
  "driver": "kubernetes",
  "connection": {
    "context": "gateway-in-cluster",
    "kubeconfig": "/tmp/gateway-in-cluster.kubeconfig",
    "namespace": "gateway",
    "route_mode": "pod-ip"
  }
}
```

The default Alpine 3.22 build installed `kubectl` 1.33.1 in the earlier local
smoke. For the OrbStack Kubernetes 1.35.6 check, Alpine 3.23 packaged
`kubectl` 1.34.2 and passed the live worker test:

```sh
docker build -f infra/docker/Gateway.provision.Dockerfile --target kubernetes \
  --build-arg GATEWAY_WORKER_ALPINE_VERSION=3.23 \
  -t gateway-provision-k8s-smoke:local .
docker build -f infra/docker/planner-fixture.Dockerfile \
  -t gateway-k8s-planner-smoke:local .
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-k8s-router-smoke:local .
docker build -f infra/docker/Gateway.tls-edge.Dockerfile \
  -t gateway-tls-edge:local .
bash scripts/test-kubernetes-provisioning
```

The smoke requires the `centrifugo/centrifugo:v6` image in the OrbStack Docker
daemon and the `gateway-tls-edge:local` image built above. It creates and
deletes a uniquely named namespace and temporarily exposes two random
NodePorts on the local OrbStack node, so run it only on the intended local
`orbstack` context. It checks the packaged worker's
ServiceAccount token and namespaced Pod RBAC, starts a healthy Centrifugo Pod
through the real Planner and worker, then SIGKILLs the exact Docker container
backing that disposable worker Pod and verifies a new worker session adopts
the same Centrifugo Pod UID and refreshes its heartbeat. The production
standalone Spinner image also queries the private Planner and proxies a real
WebSocket upgrade to that Pod before and after recovery. The test first uses
a CIDR that excludes the Pod IP and expects 502, then permits the exact Pod
IP. It checks the route and private Planner isolation through the in-cluster
Service, a host-to-node HTTP NodePort, and a separate Caddy TLS edge with a
temporary self-signed certificate trusted by the host client. It does not
test an external LoadBalancer, public internet reachability, production
certificate issuance/rotation, or multi-node scheduling.

Before publishing, check the packaged client against the target API server's
minor version; Kubernetes supports `kubectl` within one minor version of the
API server. `GATEWAY_WORKER_ALPINE_VERSION` is a build argument for selecting
an Alpine release with a suitable packaged client. See the official
[version-skew policy](https://kubernetes.io/releases/version-skew-policy/).

`infra/kubernetes/provision` supplies a single-replica, `Recreate` worker
Deployment and a namespace-scoped ServiceAccount/Role/RoleBinding. Replace its
image and private Planner URL, then create the `provision-worker-target` Secret
with `target-id` and `worker-token` keys from the one-time target response.
Keep the Secret out of source control. The example runs in namespace `gateway`;
if changing it, update the Kustomization namespace, RoleBinding subject
namespace, and target's `connection.namespace` together. The Role grants Pod
get/list/watch/create/delete/deletecollection only in that namespace. Add
`create` on `pods/portforward` only if using `route_mode=port-forward`.
The worker Pod uses a read-only root filesystem with a writable `/tmp` volume.
Workload images and any application configuration still need to be supplied
separately. This manifest does not publish or route managed workload traffic.

The image's generated kubeconfig and client/integration packaging also pass a
read-only-root-filesystem smoke with dummy ServiceAccount paths. Its image
declares numeric UID/GID 10001 so Kubernetes can enforce `runAsNonRoot`.

### Route managed workloads from a separate Spinner

The dedicated worker does not expose an HTTP listener or share its in-process
route table with the public Spinner. On a separately deployed **HTTP-mode**
Spinner, opt into Planner-backed managed routing:

```sh
GATEWAY_RUNTIME=http
GATEWAY_PLANNER_URL=http://planner.internal:8080
GATEWAY_INTERNAL_TOKEN=YOUR_PRIVATE_INTERNAL_TOKEN
GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED=true
GATEWAY_PROVISIONING_ROUTE_CIDRS=10.244.0.0/16
```

For each inbound HTTP or WebSocket request, Spinner asks Planner over its
private connection whether a managed server owns the host and path. Planner
chooses a current running instance only if its revision, instance heartbeat,
and provisioning-node lease are current. An unmatched request continues to
normal Web handling; a matched server without a healthy instance returns 503.
Spinner proxies to the instance IP and port reported by the worker, so its
network must be able to reach those addresses. Set
`GATEWAY_PROVISIONING_ROUTE_CIDRS` to a comma-separated list of only the
workload networks this Spinner may reach; use `/32` or `/128` for an exact
host. Routes outside those ranges fail closed. In particular, a Kubernetes
Pod IP is generally usable from a Spinner inside the same cluster, but a
worker-local `port-forward` loopback address is not usable by a separate
Spinner. Use `route_mode=pod-ip` for this deployment shape.

The example CIDR is illustrative; use your cluster's actual Pod network.
This mode requires the shared `GATEWAY_INTERNAL_TOKEN`; a target worker token
cannot query the cross-target route lookup. Route lookup has no local cache,
so Planner is on the request path and loss of its private connection fails
managed requests closed. The combined FrankenPHP-library and RoadRunner
Routers use the same `GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED` and
`GATEWAY_PROVISIONING_ROUTE_CIDRS` settings, resolving the lookup through
their in-process Planner handler. For the Caddy Gateway handler, set
`managed_route_cidrs 10.244.0.0/16` in its `gateway` block instead; the
public Caddyfile example leaves this commented out until an operator chooses
the actual reachable workload ranges. Use a dedicated workload hostname so a
root-path managed server does not claim Planner management paths.

Both bundled Router packages have a container-to-container managed HTTP and WebSocket smoke:
`bash scripts/test-router-managed-route gateway-router:roadrunner-local` or
`bash scripts/test-router-managed-route gateway-router:local` for the FrankenPHP-library image.
It uses the bundled Planner, separate HTTP and WebSocket backend containers,
and exact-IP allowlists. It verifies HTTP forwarding, a WebSocket upgrade and
echoed message, private-route isolation, out-of-range destination refusal,
and stale-instance fail-closed behavior. The smoke requires Docker and a local
Go toolchain, and the WebSocket fixture runs from a disposable Go container.
The custom FrankenPHP/Caddy handler also has a container-to-container smoke:
`bash scripts/test-caddy-managed-route GATEWAY_CADDY_IMAGE PLANNER_FIXTURE_IMAGE`.
It uses a separate Laravel Planner fixture behind Caddy's next handler and
checks the same HTTP/WebSocket routing and fail-closed behavior. This verifies
the Caddy module path, not a single-image Caddy+Planner deployment. The
OrbStack smoke above also verifies the production standalone Spinner's managed
WebSocket route to a real Kubernetes Pod through its Service and host NodePort
before and after worker recovery. A certificate-validated local TLS edge
passes too; external ingress and production certificate operations remain
untested.
Other local integration checks exercise Planner, the private route
lookup, a separate Go proxy, and real Docker workloads before and after
provisioning-worker recovery, including Reverb's WebSocket handshake. These
checks do not prove cross-host reachability or TLS ingress.

To create a workload, include `target_id` in the existing `POST /servers` body.
The server driver is inferred from its target; a contradictory driver is rejected.
Then use `/servers/{server}/start`, `/stop`, `/restart`, `/scale`, `/health` and
`/instances?server_id=...` as usual. Target workers receive only that target's
assignments. Legacy workers receive only untargeted assignments.

The same target settings also work in HTTP Spinner with provisioning enabled,
if traffic proxying and provisioning are intentionally colocated.

## Permissions and operational limits

- Target tokens authorize only target configuration, assignment heartbeats,
  instance reports, and resets for that target. They are not registry tokens or
  infrastructure credentials. Use HTTPS or a trusted private transport.
- Docker access remains privileged on the selected daemon. Kubernetes requires
  namespace-scoped Pod get/list/watch/create/delete/deletecollection permissions;
  port-forward mode additionally needs create on `pods/portforward`. The workload
  service account setting is separate from the worker's provisioning credential.
- Each worker process claims a unique node session before it resets reports or
  reconciles labelled resources. Planner refuses a second live session with the
  same node ID, and rejects stale reports and resets. Graceful shutdown releases
  the claim; after a crash, a replacement waits for the placement lease to
  expire. This fences Planner state, but does not yet fence external Docker or
  Kubernetes operations from a partitioned old process. An adopted Kubernetes
  handle now checks the Pod UID during adoption, watching, and before stopping,
  and refuses to act on a different Pod with the same name. This is a
  best-effort identity check, not an atomic conditional deletion: `kubectl
  delete` does not perform resource-version checks.
  Apply Planner migrations before upgrading a worker: the new Spinner requires
  Planner to echo its accepted session ID and refuses an older Planner that
  cannot confirm it. A live session returns HTTP 423 and is retried until the
  lease expires; a node ID already bound to another target remains a permanent
  HTTP 409. Workers without the new session protocol retain legacy behavior,
  so upgrade them before relying on this fence.
- After claiming its session, the worker resets stale instance reports and
  fetches desired assignments. Only after a successful assignment response does
  it remove resources labelled for this node and target that are no longer
  desired (including stopped containers and terminal Pods). A matching running
  Docker container or Kubernetes Pod is adopted: the worker verifies its
  identity, image, and endpoint, attaches a new watcher, restores its route,
  and reports it running without recreating it. Pod-IP routing can reattach
  directly; port-forward routing starts a new local port-forward to the same
  Pod. A conflicting desired resource fails closed instead of being adopted.
  Graceful shutdown and assignment-lease expiry still stop workloads, so this
  is **crash-restart recovery, not zero-downtime handoff**. There is a routing
  interruption while the worker is absent. Driver-level adoption and a hard
  worker-process crash/restart have been checked against a local Docker daemon.
  The crash test runs both with a lease-enforcing HTTP fixture and with the
  real PHP Planner and isolated SQLite data. A second real-Planner smoke runs
  the Centrifugo workload adapter and checks the same container's HTTP health
  before and after adoption; the same flow has also passed with Centrifugo
  loaded as an executable integration. Equivalent local-Docker recovery tests
  also pass with Mercure and Reverb executable integrations; the Reverb test
  verifies a managed application's WebSocket handshake before and after worker
  replacement. A disposable single-node OrbStack Kubernetes cluster has now
  verified packaged-worker Centrifugo Pod adoption after a hard container
  crash and a standalone Spinner's managed WebSocket upgrade to that Pod
  before and after adoption, both in-cluster and through a host NodePort and
  certificate-validated local TLS edge. Remote clusters, cross-node recovery,
  and public-internet ingress remain unverified.
- Token rotation immediately rejects the old token. Update the worker secret
  and restart it; an unchanged worker will stop workloads when its lease expires.
- Separate HTTP Spinner and combined Router runtimes can use the opt-in
  Planner-backed route lookup described above. The legacy Caddy handler uses
  its `managed_route_cidrs` directive. DNS/TLS/ingress automation is not included.
- A worker needs a private HTTP path to Planner. Separate Planner deployments
  use their private Planner Service. Bundled library, RoadRunner, and Caddy
  Router launchers bind `GATEWAY_ROUTER_CONTROL_LISTEN` to
  `127.0.0.1:5053` by default. This listener forwards only
  `/_internal/provisioning` routes to the bundled Planner; the public Router
  still returns 404 for all `/_internal` routes. For a worker in another
  container or host, bind this listener to a trusted private interface and
  set the worker's `GATEWAY_PLANNER_URL` to that private origin.
  Do not publish port 5053 through public ingress. Enforce network policy or
  host firewall restrictions, and use TLS or a trusted private transport.
  Planner still authenticates target-worker and internal-token requests.
- A management UI, persisted operation logs, streamed workload logs, cloud machine
  creation, bare-host bootstrapping, graceful handoff, cross-host recovery
  verification, and live Kubernetes worker deployment/recovery verification
  remain follow-up milestones.

Verification uses local PHP integration tests and Go tests with fake remote
command runners and loopback listeners. With a local Docker daemon and the
`nginx:alpine` image already available, run
`GATEWAY_TEST_DOCKER_ADOPTION=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestDockerDriverAdoptsRealContainer$' -count=1`
to check that a new driver adopts a uniquely named running container without
recreating it. Run
`GATEWAY_TEST_DOCKER_WORKER_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestDockerWorkerRecoversAfterCrash$' -count=1`
to hard-kill one worker process and verify that a replacement resumes the same
container after the mocked Planner lease expires. Run
`GATEWAY_TEST_PHP_DOCKER_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestDockerWorkerRecoversWithRealPlanner$' -count=1`
to exercise the same hard restart through the PHP Planner's actual assignment,
session, reset, and report routes. This last test uses the Centrifugo server
type but runs a lightweight nginx test workload. Run
`GATEWAY_TEST_CENTRIFUGO_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestCentrifugoWorkerRecoversWithRealPlanner$' -count=1`
to repeat the restart with the real Centrifugo workload adapter and a locally
available `centrifugo/centrifugo:v6` image. It checks Centrifugo HTTP health
before and after adoption. This test registers the adapter directly in the Go
worker. Run
`GATEWAY_TEST_CENTRIFUGO_INTEGRATION_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestCentrifugoIntegrationRecoversWithRealPlanner$' -count=1`
to build and load the Centrifugo executable through `GATEWAY_INTEGRATION_PATHS`
for both worker processes and repeat the same recovery/health assertions.
Run
`GATEWAY_TEST_MERCURE_INTEGRATION_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestMercureIntegrationRecoversWithRealPlanner$' -count=1`
for the Mercure executable and its HTTP health endpoint. Run
`GATEWAY_TEST_REVERB_INTEGRATION_RECOVERY=1 GOWORK=off go -C pkg/spinner test -mod=mod ./provision -run '^TestReverbIntegrationRecoversWithRealPlanner$' -count=1`
to build a disposable Laravel Reverb image with an isolated Planner snapshot,
load the Reverb executable, and verify that its managed application accepts a
WebSocket handshake before and after adoption. Reverb requires a Docker image
with the Laravel fixture and Composer dependencies; the test builds its own
from the locally available `sail-8.4/app:latest` base image.
The tests stop/remove their uniquely named containers and isolate Planner data
under a temporary directory. No production server or real remote cluster was
provisioned as part of these tests.
