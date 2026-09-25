# Deploying Gateway

Deploy Gateway as a public Go router plus a private Laravel planner.

```text
Internet -> TLS terminator -> Go Gateway router -> private network -> Laravel planner
```

Only the TLS terminator/router is public. Keep the planner as a Compose-only
service, Kubernetes ClusterIP, loopback listener, or private-VPC service.
The router uses `GATEWAY_LARAVEL_BACKEND_URL` and `GATEWAY_INTERNAL_TOKEN` to
call `POST /_internal/gateway/plan` privately.

## Router image

```bash
docker build -t webong/gateway:local .
```

The image runs as the unprivileged `gateway` user and exposes:

| Port | Purpose |
| --- | --- |
| `5001/tcp` | HTTP and WebSocket |
| `2525/tcp` | Optional SMTP |
| `5353/tcp`, `5353/udp` | Optional authoritative DNS |

The router image intentionally does not contain an arbitrary Laravel
application. Deploy your Gateway-enabled planner alongside it.

## Minimal Compose shape

```yaml
services:
  planner:
    image: your-laravel-planner
    environment:
      GATEWAY_INTERNAL_TOKEN: ${GATEWAY_INTERNAL_TOKEN}
      REGISTRY_TOKEN: ${REGISTRY_TOKEN}
    # Do not publish this service's ports.

  gateway:
    image: webong/gateway:local
    environment:
      GATEWAY_RUNTIME: http
      GATEWAY_LARAVEL_BACKEND_URL: http://planner:8080
      GATEWAY_INTERNAL_TOKEN: ${GATEWAY_INTERNAL_TOKEN}

  tls:
    image: caddy:2-alpine
    ports: ["443:443"]
    # Reverse proxy to gateway:5001.
```

Use a real certificate and hostname in production. `tls internal` is only for
local testing.

## Verify a deployment shape

```bash
# Router health and non-root image check.
make test-container

# Caddy TLS, private planner isolation, auth, subscription planning,
# and private-target SSRF-denial check.
make test-container-integration
```

The integration test deliberately confirms that a private Docker callback is
blocked. For a true end-to-end delivery test, use a controlled public HTTPS
receiver.

## Protocol notes

- **WebSocket:** make sure the public TLS proxy supports upgrades.
- **SMTP:** set `GATEWAY_SMTP_ADDR`; STARTTLS uses
  `GATEWAY_SMTP_TLS_CERT_FILE` and `GATEWAY_SMTP_TLS_KEY_FILE`. SMTP AUTH is
  disabled by default and requires TLS. When `GATEWAY_SMTP_RELAY_ADDR` enables
  outbound delivery, set `GATEWAY_DELIVERY_SPOOL_PATH` to persistent local
  storage and do not share that path between Gateway processes.
- **DNS:** set the DNS address, zone, nameservers, and SOA options; publish
  both UDP and TCP. Gateway is authoritative-only, not recursive.
- **Realtime:** planner state can reconcile Reverb, Mercure, and Centrifugo
  workloads via Local, Docker, or Kubernetes drivers.
