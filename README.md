# Gateway

**Receive, route and deliver network traffic.**

Gateway manages the traffic between your applications and the services they
connect to. Decide what comes in, where it goes, and how it gets delivered.

WebSockets, webhooks, webstreams, email and DNS are applications of that same
idea. Gateway provides a shared way to manage their traffic, with extensions
adding support for each.

Run Gateway as its own service and manage it through an API and command-line
tools. Add custom extensions when you need it to do more.

[Get started](docs/using-gateway.md) · [Documentation](docs/README.md) · [Deployment guide](docs/router.md)

## What can I use it for?

- **Keep apps connected.** Route WebSocket connections for chat, notifications
  and other live interactions.
- **Stream updates as they happen.** Relay SSE streams for live feeds,
  progress updates and dashboards.
- **Deliver events to your services.** Receive a webhook once and send it to
  the services that need it. Choose which events each service receives and
  inspect delivery failures.
- **Run your realtime services.** Manage Reverb, Mercure or Centrifugo through
  Gateway.

For example, your app can use a WebSocket connection for chat, an SSE stream
for a live activity feed, and webhooks to notify other services when an event
occurs. Gateway handles the traffic; your services handle the application logic.

Start with the setup guides for [WebSocket](docs/using-gateway.md#websocket),
[SSE](docs/using-gateway.md#webstream-sse), and
[managed realtime services](docs/using-gateway.md#managed-realtime-services).

## Try it locally

You need Docker with Compose. Save this as `compose.yaml`:

```yaml
services:
  gateway:
    image: webong/gateway:latest
    environment:
      GATEWAY_ROUTER_LISTEN: ":8080"
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - gateway-data:/data

volumes:
  gateway-data:
```

Start Gateway and check that it responds:

```sh
docker compose up -d
curl -fsS http://127.0.0.1:8080/health
```

This is a local trial. For a public server, follow the [HTTPS setup guide](docs/using-gateway.md#1-start-gateway)
and choose a specific published image version. Keep the data volume across
restarts and upgrades.

### Receive your first webhook

Create an endpoint called `orders`, then tell Gateway where to deliver its events:

```sh
docker compose exec -T gateway \
  /gateway/scripts/gateway-router artisan gateway:endpoint orders

docker compose exec -T gateway \
  /gateway/scripts/gateway-router artisan gateway:subscribe orders orders-service \
  https://receiver.example.net/hooks/orders
```

Replace the receiver URL with a public HTTPS endpoint you control. Gateway
blocks private and loopback delivery addresses by default.

Send a test event:

```sh
curl -i http://127.0.0.1:8080/hooks/orders \
  -H 'Content-Type: application/json' \
  --data '{"event":"order.created","order_id":"ord_123"}'
```

Check your receiver's logs for the event. The [full walkthrough](docs/using-gateway.md#3-receive-a-webhook)
shows how to inspect subscriptions and failed deliveries, and explains
endpoint validation and authentication.

## Run it as a service

Gateway runs alongside your applications. Your apps send traffic to it or receive
traffic from it, using the protocols they already support.

The bundled **Gateway Router** includes everything needed to run Gateway as one
service. Start with the Docker setup above, or follow the
[deployment guide](docs/router.md) for other installation options.

Configure endpoints, destinations and enabled extensions through Gateway's
management API and command-line tools.

## Build on Gateway with extensions

Custom extensions add capabilities to your Gateway service. Use them to support
a new kind of traffic, apply your own routing rules, or run work in response to
an event.

Package the extra behavior as an extension, then install and enable it in
Gateway. Your applications continue connecting to Gateway as a service.

- [How extensions work](docs/planner-extensions.md).
- [Build an extension package](mod/builder/README.md).
- [Existing extensions](pkg/) for implementation examples.

## Where to go next

- [Set up and use Gateway](docs/using-gateway.md) — deployment, management access,
  and feature setup.
- [Track deliveries](docs/deliveries.md) — delivery status, failures and retries.
- [Connect a third-party app](docs/third-party-subscription-guide.md) — endpoints,
  subscriptions and callback examples.
- [Run on Kubernetes](docs/kubernetes.md).
- [Technical reference](docs/technical-reference.md) — architecture, package
  layout, configuration, build commands and tests.

## Development

Working on Gateway itself? Start with the [technical reference](docs/technical-reference.md#tests)
for dependency installation and test commands. The repository contains the
Gateway service, its extensions and the tools used to build and run them.

## License

[MIT](LICENSE).
