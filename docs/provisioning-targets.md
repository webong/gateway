# Deployment targets: first milestone

Gateway can register an existing remote Docker host or Kubernetes cluster as a
target. Planner owns target records and placement; a Go provisioning worker
executes the existing Reverb, Mercure, and Centrifugo workloads there. This does
not create cloud VMs or install Docker/Kubernetes on a bare server.

The design keeps the target connection in the provisioning
module, independent of technology extensions. Extensions still import the
public driver contracts, not Spinner implementation code.

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
go build -mod=readonly -o /tmp/gateway-provision ./src/spinner/cmd/provision

export GATEWAY_LARAVEL_BACKEND_URL=https://planner.example.com
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

Build or install an executable extension and supply its workload settings, for example:

```sh
go build -mod=readonly -o /tmp/gateway-centrifugo ./ext/centrifugo/cmd/plugin
export GATEWAY_EXTENSION_PATHS=/tmp/gateway-centrifugo
export GATEWAY_CENTRIFUGO_DOCKER_IMAGE=YOUR_PINNED_CENTRIFUGO_IMAGE
/tmp/gateway-provision
```

For Kubernetes use `GATEWAY_CENTRIFUGO_KUBERNETES_IMAGE` instead. See each
extension's README for image requirements and launch settings. The dedicated
worker opens no inbound network listener and does not embed PHP or QuickJS.
Credentials need only be installed on the worker, not on the public router.

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
- Startup still uses the existing destructive reconciliation model: clean up
  resources labelled for this node (and target), reset reports, then recreate
  desired instances. Shutdown and assignment-lease expiry stop workloads. This
  is **not yet durable adopt-on-restart orchestration**. Plan for interruption.
- Token rotation immediately rejects the old token. Update the worker secret
  and restart it; an unchanged worker will stop workloads when its lease expires.
- This worker controls deployment only. Its instance addresses do not configure
  a separate public Router's route table. Arrange private routing/ingress yourself,
  or use the colocated HTTP Spinner mode. DNS/TLS/ingress automation is not included.
- Connect to an externally reachable Planner HTTP host. Bundled FrankenPHP and
  RoadRunner Router public listeners still hide `/_internal`; a dedicated private
  control listener for those deployments is a follow-up, not enabled here.
- A management UI, persisted operation logs, streamed workload logs, cloud machine
  creation, bare-host bootstrapping, durable recovery, and worker container/K8s
  packaging remain follow-up milestones.

Verification in this change uses local PHP integration tests and Go tests with
fake remote command runners. No production server or real remote cluster was
provisioned as part of these tests.
