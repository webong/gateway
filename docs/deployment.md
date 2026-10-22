# Deploying Gateway

For a single-service deployment such as Zorvia's Mail integration, use the
[bundled RoadRunner Router](router.md#roadrunner-docker-image): one image with
Spinner and Planner, one private `/data` volume, HTTPS on 8443, and TLS IMAP on
993. Planner APIs own the mailbox
connections and policies. No separate PHP Planner or PostgreSQL service is
needed for a single local replica. Do not point this deployment at the
Go-only `webong/gateway:sha-545d432` image.

## Published image tags

`webong/gateway` is published as the complete Gateway application. Public tags
identify only the product release:

- `latest` tracks the newest published release.
- `sha-<commit>` is the immutable build for a Git commit.
- `v<version>` is an immutable named release.

Do not publish capability, runtime, architecture, or transport names such as
`mail`, `router`, `javascript`, `arm64`, RoadRunner, or FrankenPHP in the tag.
The public tag points to one multi-platform manifest for `linux/amd64` and
`linux/arm64`. Runtime composition is image metadata and implementation detail,
not a different Gateway product.

The full Router build includes JavaScript, Lua, WebhookScript, Web, Email, DNS,
and the other installed Gateway surfaces. The publisher defaults to the bundled
RoadRunner composition used by Zorvia; selecting another composition does not
change the repository or tag. Publish from a clean commit with:

```bash
scripts/publish-gateway-image
# Or publish a named release as well as latest:
scripts/publish-gateway-image v1.0.0
```

The split deployment below remains available when Spinner and Planner need to
scale independently:

```text
Internet -> TLS terminator -> Spinner (Go) -> private network -> Planner (PHP)
```

Only the TLS terminator/router is public. Keep the planner as a Compose-only
service, Kubernetes ClusterIP, loopback listener, or private-VPC service.
Spinner uses `GATEWAY_PLANNER_URL` and `GATEWAY_INTERNAL_TOKEN` to
call `POST /_internal/gateway/plan` privately.

## Spinner image

```bash
docker pull webong/gateway:sha-545d432
# Or build the same router locally from this checkout:
docker build -t webong/gateway:local .
```

The published `webong/gateway` image contains the Go Spinner/router for
`linux/amd64` and `linux/arm64`, not the PHP Planner. Pin a release tag (or its
digest) in Compose; `latest` may move. Both processes must share a private
network and the same `GATEWAY_INTERNAL_TOKEN`.

The image runs as the unprivileged `gateway` user and exposes:

| Port | Purpose |
| --- | --- |
| `5001/tcp` | HTTP and WebSocket |
| `2525/tcp` | Optional SMTP |
| `5353/tcp`, `5353/udp` | Optional authoritative DNS |

The Spinner image contains no PHP application. Deploy the Planner alongside it.

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
    image: webong/gateway:sha-545d432
    environment:
      GATEWAY_RUNTIME: http
      GATEWAY_PLANNER_URL: http://planner:8080
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
# private-target SSRF denial, and HTTP Email relay/receipt/DSN checks.
make test-container-integration

# Two local authoritative Spinner processes, shared Planner hooks, and
# UDP/TCP query survival after one process stops.
make test-dns-integration
```

The integration test deliberately confirms that a private Docker callback is
blocked. For a true end-to-end delivery test, use a controlled public HTTPS
receiver.

The Email slice of this test crosses public TLS, Spinner, Planner, the private
delivery-admin listener, and a disposable SMTP relay. It checks the envelope
and message at that relay, then confirms the delivered receipt survives a
Spinner restart, a same-key replay does not send a second copy, and a second
account cannot bypass the sender-domain quota. It also refuses one recipient
and checks the resulting null-path failure notice. This proves controlled
local relay and failure paths, not public-domain deliverability or a later
bounce after a relay accepted mail.
The Planner must trust only its private reverse proxy so Laravel can recognize
the original HTTPS scheme; do not trust arbitrary public `X-Forwarded-Proto`
headers or expose the Planner listener directly.

## Protocol notes

- **WebSocket:** make sure the public TLS proxy supports upgrades.
- **SMTP:** set `GATEWAY_SMTP_ADDR`; STARTTLS uses
  `GATEWAY_SMTP_TLS_CERT_FILE` and `GATEWAY_SMTP_TLS_KEY_FILE`. SMTP AUTH is
  disabled by default and requires TLS. When `GATEWAY_SMTP_RELAY_ADDR` enables
  outbound delivery, set `GATEWAY_DELIVERY_SPOOL_PATH` to persistent local
  storage and do not share that path between Gateway processes.
- **POP3/IMAP retrieval:** set `GATEWAY_POP3_ADDR` and/or `GATEWAY_IMAP_ADDR`,
  the corresponding TLS certificate/key files and registered upstream maps on
  Spinner. Configure a Planner
  `GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR`; without one, login is denied.
  Publish only the implicit-TLS listeners (normally ports 995 and 993) and keep the
  Spinner-to-Planner connection private. See [mail forwarding](mail-forwarding.md).
- **DNS:** set `GATEWAY_DNS_ADDR` on Spinner and enable the Planner DNS control
  plane. Spinner refreshes API-managed zones over its authenticated private
  Planner connection without restarting; the old zone environment settings are
  migration-only. Publish both UDP and TCP. Each active zone requires
  parent-zone delegation. Gateway is
  authoritative-only, not recursive. See [DNS high availability](dns-ha.md)
  for the two-node deployment and validation shape.
- **Realtime:** planner state can reconcile Reverb, Mercure, and Centrifugo
  workloads via Local, Docker, or Kubernetes drivers. A separate HTTP Spinner
  can opt into private Planner-backed routing with
  `GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED=true`; see
  [deployment targets](provisioning-targets.md#route-managed-workloads-from-a-separate-spinner).
