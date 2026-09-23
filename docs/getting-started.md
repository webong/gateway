# Getting started

This guide uses Gateway’s HTTP/webhook capability. The same endpoint and
subscription registry supports the other protocol adapters.

## Install the Laravel planner

In the Laravel application hosting Gateway's planner:

```bash
composer require webong/gateway
php artisan vendor:publish --tag=gateway-config
php artisan migrate
```

Set two separate secrets:

```dotenv
REGISTRY_TOKEN=public-management-api-token
GATEWAY_INTERNAL_TOKEN=private-router-to-planner-token
```

`REGISTRY_TOKEN` authorizes management requests. `GATEWAY_INTERNAL_TOKEN` is
only for Go router calls to private planner routes.

Configure a `PathResolver` or custom `RoutePlanner` in Laravel. It maps public
ingress paths to endpoint keys and routing scope; Gateway deliberately does
not reserve a special webhook path.

## Run the router in HTTP mode

```dotenv
GATEWAY_RUNTIME=http
GATEWAY_LARAVEL_BACKEND_URL=http://gateway-planner:8080
GATEWAY_INTERNAL_TOKEN=private-router-to-planner-token
PORT=5001
```

`gateway-planner:8080` must be private to Gateway’s deployment network.
PHP-FPM, Octane, RoadRunner, and FrankenPHP are all valid planner hosts.

## Register an endpoint and subscriber

Send management calls to the public Gateway domain:

```bash
curl -X POST https://gateway.example.com/registry/endpoints \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"client":"my-provider","external_id":"account-42","endpoint_key":"account-42","managed":true}'
```

Create a matching subscription:

```bash
curl -X POST https://gateway.example.com/registry/endpoints/account-42/subscriptions \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "subscriber_id":"orders-service",
    "subscription_id":"orders-created",
    "webhook_group":"orders",
    "routing_scope":"application",
    "routing_key":"orders",
    "url":"https://orders.example.com/hooks/provider",
    "match":{"version":"v1","rules":{"body.event":["required","in:created"]}}
  }'
```

The callback must be globally routable in production.

## Send ingress

```bash
curl -X POST https://gateway.example.com/hooks/provider \
  -H 'Content-Type: application/json' \
  --data '{"event":"created","order_id":"ord_123"}'
```

```text
provider -> public Go router -> private Laravel planner -> route plan
         -> Go delivery worker -> subscriber callback
```

The router adds a stable SHA-256
`X-Webhook-Forwarder-Delivery-Id`; Laravel selects destinations and Go
executes only those delivery instructions.
