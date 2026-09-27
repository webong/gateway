# Kubernetes deployments

Gateway ships two independent Kubernetes configurations:

| Configuration | Container | Planner | Persistent state |
| --- | --- | --- | --- |
| [`infra/kubernetes/router`](../infra/kubernetes/router) | Combined Router image from `infra/docker/Gateway.router.Dockerfile` | Bundled in-process through the FrankenPHP Go library | `router-data` PVC for SQLite, keys, cache, and storage |
| [`infra/kubernetes/spinner`](../infra/kubernetes/spinner) | Go-only image from the root `Dockerfile` | Separate Laravel service at `GATEWAY_LARAVEL_BACKEND_URL` | None for the default HTTP transport |

Build the image you need, publish it to a registry reachable by your cluster,
and replace the example `image:` value in that configuration before applying
it. The manifests intentionally do not create an Ingress or configure TLS;
attach your cluster's ingress/controller to the `router` or `spinner` Service.

## Combined Router

```sh
docker build -f infra/docker/Gateway.router.Dockerfile -t REGISTRY/router:TAG .
docker push REGISTRY/router:TAG
kubectl create namespace gateway
# Set image: REGISTRY/router:TAG in infra/kubernetes/router/deployment.yaml.
kubectl -n gateway apply -k infra/kubernetes/router
kubectl -n gateway rollout status deployment/router
```

The `router` Service exposes port 80 to the single Router Pod's port 8080.
Startup generates credentials under `/data`, applies migrations, and serves
`/health`. The Deployment uses `Recreate` with one replica so an update cannot
run two SQLite writers against the same `router-data` claim. Keep and back up
the PVC across image upgrades; losing `gateway.env` while retaining the database
prevents startup. Do not scale this SQLite configuration beyond one replica.

## Spinner alone

```sh
docker build -f Dockerfile -t REGISTRY/spinner:TAG .
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
health, not the external Planner's readiness.
