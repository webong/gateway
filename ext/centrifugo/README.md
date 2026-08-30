# Gateway Centrifugo extension

`webong/gateway-centrifugo` teaches Gateway core how to configure, launch, and
health-check Centrifugo servers.

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
  "type": "centrifugo",
  "name": "customer-events",
  "hostname": "events.example.com",
  "path": "/connection",
  "driver": "kubernetes",
  "replicas": 3,
  "configuration": {
    "client": {
      "token_hmac_secret_key": "client-secret",
      "allowed_origins": ["https://app.example.com"]
    },
    "channel": {"allow_subscribe_for_client": true},
    "http_api": {"key": "api-secret"},
    "engine": {
      "type": "redis",
      "redis_address": "redis://redis.internal:6379"
    },
    "prometheus_enabled": true,
    "log_level": "info"
  }
}
```

The token secret, API key and Redis address are encrypted in the shared server
configuration and redacted from public resources. Multiple replicas require
the Redis engine so publications and presence remain consistent across nodes.
The default public route is `/connection`, keeping Centrifugo's HTTP API,
metrics, health and admin handlers away from Gateway's public edge.

## Runtime

```text
GATEWAY_PROVISIONING_ENABLED=true
GATEWAY_CENTRIFUGO_ENABLED=true
GATEWAY_CENTRIFUGO_BINARY=centrifugo
GATEWAY_CENTRIFUGO_WORKING_DIRECTORY=.
GATEWAY_CENTRIFUGO_DOCKER_IMAGE=centrifugo/centrifugo:v6
GATEWAY_CENTRIFUGO_DOCKER_BINARY=centrifugo
GATEWAY_CENTRIFUGO_DOCKER_CONTAINER_PORT=8000
GATEWAY_CENTRIFUGO_KUBERNETES_IMAGE=centrifugo/centrifugo:v6
GATEWAY_CENTRIFUGO_KUBERNETES_BINARY=centrifugo
GATEWAY_CENTRIFUGO_KUBERNETES_CONTAINER_PORT=8000
```

Configuration is translated to the official
[`CENTRIFUGO_*` environment format](https://centrifugal.dev/docs/server/configuration).

Run `make test` for unit/API coverage, `make smoke-docker` for the live
official-image launch check, or `make smoke-kubernetes` against the active
Kubernetes context.
