# Gateway Mercure extension

`webong/gateway-mercure` teaches Gateway core how to configure, launch, and
health-check Mercure.rocks Hub servers.

## API

All routes require `Authorization: Bearer <REGISTRY_TOKEN>`:

```text
POST   /servers
GET    /servers
GET    /servers/{server}
PATCH  /servers/{server}
DELETE /servers/{server}
POST   /servers/{server}/start
POST   /servers/{server}/stop
POST   /servers/{server}/restart
POST   /servers/{server}/scale
GET    /servers/{server}/health
GET    /instances?server_id={server}
GET    /instances/{instance}
```

Example:

```json
{
  "type": "mercure",
  "name": "customer-updates",
  "hostname": "updates.example.com",
  "path": "/.well-known/mercure",
  "driver": "kubernetes",
  "replicas": 1,
  "configuration": {
    "publisher_jwt": {"key": "publisher-secret", "algorithm": "HS256"},
    "subscriber_jwt": {"key": "subscriber-secret", "algorithm": "HS256"},
    "anonymous": false,
    "cors_origins": ["https://app.example.com"],
    "publish_origins": ["https://api.example.com"],
    "subscriptions": true,
    "heartbeat": "40s",
    "transport": "local"
  }
}
```

JWT keys and the complete workload configuration are encrypted in the shared
`servers.configuration` column. Public resources only report whether each key
is configured.

The default public route is `/.well-known/mercure`, so Caddy administration and
health endpoints are not exposed through Gateway.

Mercure Community is constrained to one replica per logical server because its
community hub does not provide cross-node synchronization. Gateway can still
manage many independent Mercure servers. A future Enterprise workload mode can
relax that constraint when cluster configuration is supplied.

## Runtime

Enable both provisioning and this workload:

```text
GATEWAY_PROVISIONING_ENABLED=true
GATEWAY_MERCURE_ENABLED=true
GATEWAY_MERCURE_BINARY=mercure
GATEWAY_MERCURE_WORKING_DIRECTORY=.
GATEWAY_MERCURE_CONFIG=
GATEWAY_MERCURE_DOCKER_IMAGE=dunglas/mercure:latest
GATEWAY_MERCURE_DOCKER_BINARY=caddy
GATEWAY_MERCURE_DOCKER_CONFIG=/etc/caddy/Caddyfile
GATEWAY_MERCURE_DOCKER_CONTAINER_PORT=80
GATEWAY_MERCURE_KUBERNETES_IMAGE=dunglas/mercure:latest
GATEWAY_MERCURE_KUBERNETES_BINARY=caddy
GATEWAY_MERCURE_KUBERNETES_CONFIG=/etc/caddy/Caddyfile
GATEWAY_MERCURE_KUBERNETES_CONTAINER_PORT=80
```

The adapter follows the official [Mercure Hub installation](https://mercure.rocks/docs/hub/install)
and [configuration](https://mercure.rocks/docs/hub/config) contracts.

Run `make test` for unit/API coverage, `make smoke-docker` for the live
official-image launch check, or `make smoke-kubernetes` against the active
Kubernetes context.
