# Gateway Reverb extension

`webong/gateway-reverb` is the Laravel control-plane extension for managed
Laravel Reverb servers. It manages Reverb; it does not implement the
WebSocket/Pusher protocol.

## Ownership

- Gateway core owns the generic server/application/instance schemas, unified
  public API, desired state, encrypted credentials, health and scaling,
  hostname/path routing, reconciliation, and Local/Docker/Kubernetes drivers.
- `ext/reverb` owns Reverb-specific validation and configuration
  interpretation, its runtime workload specification, health check, and the
  dynamic Reverb application provider. Gateway core owns encrypted credential
  storage.
- Laravel Reverb owns WebSocket connections, channels, messages, application
  limits, and Redis horizontal scaling.

The package registers the `gateway` driver with Reverb's
`ApplicationManager`. A managed process receives `GATEWAY_REVERB_SERVER_ID`,
and the provider scopes `all`, `findById`, and `findByKey` to that logical
server. Missing and unknown server identities fail closed.

## Management API

All public routes require `Authorization: Bearer <REGISTRY_TOKEN>`.

```text
POST   /servers
GET    /servers
GET    /servers/{server}
PATCH  /servers/{server}
DELETE /servers/{server}

POST   /applications
GET    /applications?server_id={server}
GET    /applications/{application}
PATCH  /applications/{application}
DELETE /applications/{application}

POST   /servers/{server}/start
POST   /servers/{server}/stop
POST   /servers/{server}/restart
POST   /servers/{server}/scale
GET    /servers/{server}/health
GET    /instances?server_id={server}
GET    /instances/{instance}
```

Example server:

```json
{
  "type": "reverb",
  "name": "customer-realtime",
  "hostname": "socket.example.com",
  "path": "/reverb",
  "driver": "local",
  "replicas": 2,
  "configuration": {
    "max_request_size": 10000,
    "scaling": {
      "enabled": true,
      "channel": "gateway:reverb:customer-realtime"
    }
  }
}
```

Example application:

```json
{
  "server_id": "019d4000-0000-7000-8000-000000000001",
  "app_id": "customer-production",
  "key": "public-application-key",
  "secret": "private-application-secret",
  "allowed_origins": ["app.example.com"],
  "ping_interval": 60,
  "activity_timeout": 30,
  "max_message_size": 10000,
  "max_connections": 5000,
  "accept_client_events_from": "members",
  "rate_limiting": {
    "enabled": true,
    "max_attempts": 60,
    "decay_seconds": 60,
    "terminate_on_limit": false
  },
  "options": {
    "host": "socket.example.com",
    "port": 443,
    "scheme": "https",
    "useTLS": true
  }
}
```

Application secrets are accepted on create or rotation but omitted from every
resource response. Allowed origins use Reverb's hostname patterns, for example
`app.example.com`, `*.example.com`, or `*`.

## Shared reconciliation API

The Go runtime uses private routes authenticated by `X-Gateway-Internal` and
`GATEWAY_INTERNAL_TOKEN`:

```text
GET /_internal/provisioning/servers
POST /_internal/provisioning/instances/reset
PUT /_internal/provisioning/servers/{server}/instances/{instance}
```

These routes are blocked at the public Go edge. Reconciliation supports
`local`, `docker`, and `kubernetes` server drivers and currently requires
`GATEWAY_RUNTIME=http` so it can use the private Laravel backend.

## Runtime settings

```text
GATEWAY_SERVERS_TABLE=servers
GATEWAY_APPLICATIONS_TABLE=applications
GATEWAY_INSTANCES_TABLE=instances
GATEWAY_PROVISIONING_ENABLED=false
GATEWAY_PROVISIONING_POLL_INTERVAL=5s
GATEWAY_PROVISIONING_START_TIMEOUT=15s
GATEWAY_PROVISIONING_STOP_TIMEOUT=15s
GATEWAY_NODE_ID=<machine hostname>

# Reverb workload
GATEWAY_REVERB_ENABLED=false
GATEWAY_REVERB_PHP_BINARY=php
GATEWAY_REVERB_ARTISAN=artisan
GATEWAY_REVERB_WORKING_DIRECTORY=.

# Shared Docker driver; Reverb support is enabled when its image is set
GATEWAY_PROVISIONING_DOCKER_BINARY=docker
GATEWAY_PROVISIONING_DOCKER_NETWORK=
GATEWAY_PROVISIONING_DOCKER_ENV_FILE=
GATEWAY_PROVISIONING_DOCKER_PUBLISH_HOST=127.0.0.1
GATEWAY_PROVISIONING_DOCKER_ROUTE_HOST=
GATEWAY_REVERB_DOCKER_IMAGE=
GATEWAY_REVERB_DOCKER_PHP_BINARY=php
GATEWAY_REVERB_DOCKER_ARTISAN=artisan
GATEWAY_REVERB_DOCKER_CONTAINER_PORT=8080

# Shared Kubernetes driver; Reverb support is enabled when its image is set
GATEWAY_PROVISIONING_KUBECTL_BINARY=kubectl
GATEWAY_PROVISIONING_KUBERNETES_NAMESPACE=default
GATEWAY_PROVISIONING_KUBERNETES_SERVICE_ACCOUNT=
GATEWAY_PROVISIONING_KUBERNETES_ENV_CONFIG_MAP=
GATEWAY_PROVISIONING_KUBERNETES_ENV_SECRET=
GATEWAY_PROVISIONING_KUBERNETES_IMAGE_PULL_POLICY=IfNotPresent
GATEWAY_PROVISIONING_KUBERNETES_ROUTE_MODE=pod-ip
GATEWAY_REVERB_KUBERNETES_IMAGE=
GATEWAY_REVERB_KUBERNETES_PHP_BINARY=php
GATEWAY_REVERB_KUBERNETES_ARTISAN=artisan
GATEWAY_REVERB_KUBERNETES_CONTAINER_PORT=8080
```

The shared tables are configured through `gateway.servers.tables.servers`,
`.applications`, and `.instances`.
Their defaults are the generic table names shown above. Configure the names
before running the Gateway core migrations.

Redis connection settings are inherited by each Reverb process. Replicas of a
logical server share its scaling channel. Separate logical servers receive
different channels by default. A server may override the inherited Redis
connection with `scaling.server`; URL and password values are encrypted and
redacted from public resources.

The Docker image and Kubernetes image must contain the Laravel application,
Reverb, and this extension. Docker additionally requires daemon access; the
optional environment file supplies application configuration. Kubernetes uses
the active `kubectl` context, requires Pod `get`, `create`, `delete`, and
`deletecollection` permissions, and can inherit configuration from a ConfigMap
and Secret. Pod IPs must be reachable from Gateway.

Use `pod-ip` routing when Gateway can reach the cluster Pod network. Use
`port-forward` when Gateway runs outside that network; Gateway then maintains a
node-local `kubectl port-forward` process for each managed Pod.

At startup, Gateway marks active instance records for its node as stopped and
removes Docker containers or Kubernetes Pods carrying that node's ownership
label before rebuilding the desired replica set. This prevents persistent
runtimes from being duplicated after a Gateway restart.

Multiple Gateway nodes may share one control-plane database. Every node sends
an authenticated assignment heartbeat that declares its available runtime
drivers. The control plane elects the lexicographically first live node as the
placement leader and deterministically assigns each replica slot only to a
compatible node. Nodes reconcile only their assigned slots, so a node restart
does not duplicate another node's workload. Set the same
`GATEWAY_PROVISIONING_COORDINATION_LEASE_SECONDS` value on every node; a
departed node's slots are reassigned after that lease expires.

## Tests

Run the extension's Go and PHP tests from the repository root:

```bash
make -C ext/reverb test
```

Run the local-process integration smoke. It creates two logical servers with
three total replicas, completes Pusher WebSocket handshakes through both
Gateway routes, and stops every instance:

```bash
make -C ext/reverb smoke
```

With an active local Kubernetes context backed by the same Docker image store,
run the Kubernetes integration smoke. It creates two logical servers with
three total Pods, verifies both routes and both primary replicas, then removes
the temporary namespace and image:

```bash
make -C ext/reverb smoke-kubernetes
```
