# Set up and use Gateway

This guide is for an operator deploying Gateway and configuring it for
application traffic. It covers the combined Docker image, a first Webhook, and
the setup path for WebSocket, Webstream (SSE), DNS, Email, automations, and
managed realtime services.

Gateway is managed through an HTTPS API and a small command-line interface.
The bundled distribution does not currently include an admin dashboard.

## 1. Start Gateway

You need Docker with Compose and a hostname whose DNS points to this server.
For a public deployment, allow inbound TCP ports 80 and 443 for TLS.

Create a Compose file:

~~~yaml
services:
  gateway:
    image: webong/gateway:latest
    restart: unless-stopped
    environment:
      GATEWAY_ROUTER_LISTEN: ":8080"
    expose:
      - "8080"
    volumes:
      - gateway-data:/data

  caddy:
    image: caddy:2
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config

volumes:
  gateway-data:
  caddy-data:
  caddy-config:
~~~

Create a file named Caddyfile beside it:

~~~text
gateway.example.com {
    reverse_proxy gateway:8080
}
~~~

Replace gateway.example.com with your hostname. Caddy obtains and renews the
public TLS certificate. Only Caddy publishes ports to the internet; Gateway
is reachable by the proxy on the Compose network.

Start it and check that Gateway responds:

~~~sh
docker compose up -d
curl -fsS https://gateway.example.com/health
~~~

For a local-only trial, publish Gateway on loopback instead of adding Caddy:

~~~yaml
services:
  gateway:
    image: webong/gateway:latest
    restart: unless-stopped
    environment:
      GATEWAY_ROUTER_LISTEN: ":8080"
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - gateway-data:/data

volumes:
  gateway-data:
~~~

Then check http://127.0.0.1:8080/health. This local setup uses plain HTTP and
must not be exposed directly to the internet.

Use a specific published release tag instead of latest for production. Keep
the gateway-data volume when upgrading or replacing the container.

## 2. Get the management API token

Gateway creates its initial credentials in the persistent data volume. Get
the management token from the running container:

~~~sh
export REGISTRY_TOKEN="$(docker compose exec -T gateway \
  /gateway/scripts/gateway-router artisan gateway:token)"
export GATEWAY_URL="https://gateway.example.com"
~~~

For a local-only trial, set GATEWAY_URL to http://127.0.0.1:8080.

Treat REGISTRY_TOKEN like an administrator password. It authorizes management
requests. Do not put it in a browser app, commit it, or give it to an event
sender. Keep the data volume: it holds Gateway's database, credentials,
delivery state, and installed add-ons.

The same token is sent as a bearer token for management calls:

~~~sh
curl -fsS -H "Authorization: Bearer $REGISTRY_TOKEN" \
  "$GATEWAY_URL/extensions"
~~~

## 3. Receive a Webhook

Create an endpoint and subscribe a public HTTPS receiver. These commands are
available in the combined Docker image:

~~~sh
docker compose exec -T gateway \
  /gateway/scripts/gateway-router artisan gateway:endpoint orders

docker compose exec -T gateway \
  /gateway/scripts/gateway-router artisan gateway:subscribe orders orders-service \
  https://receiver.example.net/hooks/orders
~~~

Replace the receiver URL with an HTTPS address your service controls. Gateway
blocks private, loopback, and link-local callback addresses, so a service name
inside this Compose network is not a valid production callback.

Send a test event through the public route:

~~~sh
curl -i -X POST "$GATEWAY_URL/hooks/orders" \
  -H 'Content-Type: application/json' \
  --data '{"event":"order.created","order_id":"ord_123"}'
~~~

The endpoint key determines the path: the example receives traffic at
/hooks/orders. Confirm the receiver logs show the callback. An ingress response
alone does not prove that the receiver processed it.

You can inspect subscriptions and delivery failures with the management token:

~~~sh
curl -fsS -H "Authorization: Bearer $REGISTRY_TOKEN" \
  "$GATEWAY_URL/registry/endpoints/orders/subscriptions"

curl -fsS -H "Authorization: Bearer $REGISTRY_TOKEN" \
  "$GATEWAY_URL/deliveries?state=failed&limit=50"
~~~

Planner also manages how an endpoint treats HTTP requests. Existing endpoints
use the `webhook` route kind. Set `route_kind` to `http` on endpoint creation,
or change it later with the management API:

~~~sh
curl -fsS -H "Authorization: Bearer $REGISTRY_TOKEN" \
  "$GATEWAY_URL/registry/endpoints/orders"

curl -fsS -X PATCH "$GATEWAY_URL/registry/endpoints/orders" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"route_kind":"http"}'
~~~

Both route kinds can select subscriptions and HTTP targets. `webhook` uses the
Web extension's event policy and history; `http` uses core Planner routing.
Unbound paths pass through to the configured HTTP host. The management token
is required to change an endpoint's route kind.

A subscription created with the command above accepts all events for that
endpoint. For production, use matching rules to limit which events reach each
receiver. See the [third-party app setup walkthrough](third-party-subscription-guide.md),
[subscription matching](subscription-dsl-v1.md), and the
[management API examples](examples/subscriptions/README.md).

## 4. Enable a Gateway feature

HTTP Webhooks work with the combined image. WebSocket, Webstream, DNS, Email,
and automation languages use separately released Gateway add-ons. Enable the
add-on from the management API before sending traffic for that feature.

For an add-on, select the release asset for your Gateway version and server
architecture, then create an activation at POST /extensions. Include its
published version, HTTPS download URL, SHA-256 digest from the matching
release sidecar, enabled=true, and any feature settings. The response from
GET /extensions shows each add-on's state. Use the exact request shape in
[extension installation](planner-extensions.md#github-release-artifacts).

For example, activate a DNS release and set its listener with this request.
Replace the version, architecture, and digest with values from the same public
Gateway release.

~~~sh
curl -fsS -X POST "$GATEWAY_URL/extensions" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "extension":"webong/dns",
    "enabled":true,
    "version":"1.2.3",
    "artifact":{
      "url":"https://github.com/webong/gateway/releases/download/v1.2.3/webong-dns-1.2.3-linux-amd64.tar.gz",
      "sha256":"<64-character-sha256-from-the-matching-sidecar>"
    },
    "configuration":{"address":":5353"}
  }'
~~~

Use linux-arm64 instead of linux-amd64 for an ARM64 server. The request uses
the same REGISTRY_TOKEN as other management calls. Once activated, Gateway
downloads and verifies the release; you do not rebuild the image. Keep the
add-on version pinned. Check GET /extensions for its state. If an add-on reports
unavailable or incompatible, do not route production traffic to it until the
status is ready.

## 5. Choose what you want Gateway to do

### WebSocket

Enable the webong/web add-on. Gateway supports both WebSocket ingress and
WebSocket delivery targets:

- WebSocket clients can connect to Gateway at a configured route. Gateway sends
  connection, message, and close events to Planner; an `http` subscription can
  deliver those events to an application callback.
- An HTTP route can deliver each matching event to your application's
  WebSocket server by creating a `websocket` subscription target. Gateway dials
  the configured `ws://` or `wss://` address, sends the HTTP request body as one
  WebSocket frame, and closes that connection. This is an asynchronous `relay`
  target, not an HTTP `reply` target.

For example, after creating the `orders` endpoint and granting the route to an
account, register a WebSocket destination for the same ingress path:

~~~sh
curl -fsS -X POST "$GATEWAY_URL/registry/endpoints/orders/subscriptions" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "subscriber_id":"orders-realtime",
    "subscription_id":"orders-realtime",
    "type":"relay",
    "group":"orders",
    "routing_scope":"path",
    "routing_key":"/hooks/orders",
    "target_kind":"websocket",
    "url":"wss://realtime.example.net/gateway/orders"
  }'
~~~

The application must accept outbound WebSocket connections from Gateway.
Gateway sends the original HTTP body as a text frame for textual content types
and uses valid UTF-8 as a fallback when the content type is missing or invalid;
otherwise it sends a binary frame. It includes
`X-Gateway-Event-ID` in the handshake so the application can deduplicate
retries. A lost connection after the server receives a frame can still result
in an uncertain outcome. Gateway stops automatic retries in that case because
the application may already have processed the frame. If callers replay an
ingress event, the application can use the event ID to deduplicate it.

Use the subscription's endpoint-scoped `profile` to supply handshake headers
such as an authorization token. Gateway never forwards the incoming HTTP
request's authorization headers into the WebSocket handshake. Profiles that
configure an HTTP proxy are rejected for this target because the proxy could
bypass Gateway's destination network checks. Public target addresses are
allowed by default. To dial private application addresses, add
the permitted ranges to the Planner-managed
`configuration.websocket.outbound_private_network_cidrs` setting on the active
`webong/web` extension.

### Webstream (SSE)

Enable the webong/web add-on. Create an endpoint and destination as in the
Webhook example, with the destination set to your application's event-stream
URL. Keep the route on HTTPS in production. For normal GET-based SSE, let the
stream remain open and verify that events reach the client.

Webstream route settings are available at /webstream/routes. Use them only
when the route needs streamed request uploads or idempotent retries. A route
setting names the exact public host and path and the matching source URL; upload
and retry are off unless enabled. When retries are enabled for POST, the client
must send an Idempotency-Key and the source must honor it. Private application
sources also require an explicit restricted grant at
/webstream/private-sources. Do not allow broad private network ranges.

### DNS

Enable the DNS add-on before creating zones. Set its listener to :5353 in the
add-on settings. If you only need DNS for another Gateway feature, DNS can run
without publishing a public DNS listener. To serve a public zone, add these
ports to the Gateway service in Compose:

~~~yaml
ports:
  - "53:5353/udp"
  - "53:5353/tcp"
~~~

Both transports are required for public authoritative DNS. The machine must
be able to receive port 53 from the internet.

Create a zone with the nameservers that will answer for it:

~~~sh
curl -fsS -X POST "$GATEWAY_URL/dns/zones" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "name":"example.com",
    "nameservers":["ns1.example.net","ns2.example.net"]
  }'
~~~

Add the zone's required A, AAAA, CNAME, MX, and TXT records through
PUT /dns/zones/{zone}/records. This replaces the complete record list. Fetch
the zone first and send the returned ETag in If-Match:

~~~sh
curl -i -fsS "$GATEWAY_URL/dns/zones/ZONE_ID" \
  -H "Authorization: Bearer $REGISTRY_TOKEN"

curl -fsS -X PUT "$GATEWAY_URL/dns/zones/ZONE_ID/records" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H "If-Match: $ZONE_ETAG" \
  -H 'Content-Type: application/json' \
  --data '{"records":[
    {"name":"example.com","type":"A","value":"203.0.113.10"},
    {"name":"www.example.com","type":"CNAME","value":"example.com"}
  ]}'
~~~

The example IP is reserved for documentation; replace it and the nameservers
with your real public DNS values. Use the exact ETag returned by the zone
request. Gateway manages the zone's NS and SOA records.

At your DNS provider or registrar, point the zone at the Gateway nameservers.
Create address records for those nameservers first, and add glue records when
the nameserver is inside the zone being delegated. Check both nameservers over
UDP and TCP before moving a live domain. The
[DNS delegation checklist](dns-ha.md) includes the verification commands and
the requirements for two-node production DNS.

To forward selected DNS questions to another resolver instead, create a
forwarder at /dns/forwarders and restrict both its domain suffixes and client
networks. Do not create a public catch-all resolver.

### Email

Enable both the DNS and Email add-ons before using Gateway Email. DNS can stay
resolver-only when you are not hosting public zones. Then choose the flow you
need:

- To receive mail for an application, verify a domain, create a receiving
  route, publish the required MX records, and set the route's HTTPS callback.
- To send mail, configure an outbound connection, verify the sending domain,
  publish the requested SPF and DKIM records, and check DMARC before sending
  real user mail.
- To connect an existing mailbox provider, add its IMAP/SMTP connection and
  test the connection before routing messages through it.

Gateway handles mail transport and routing; it does not store full mailboxes
or provide webmail. See [Email routes and mailbox connections](mail-forwarding.md)
and the [Email authentication checklist](email-authentication.md) for the
details required before public mail flow.

Publish only the Email listener ports you configured. For example, if the
Email settings listen on container port 2525, publish SMTP on the standard
public port by adding this to the Gateway service:

~~~yaml
ports:
  - "25:2525/tcp"
~~~

SMTP, IMAP, and POP3 are not started just because their ports are mapped in
Compose. Use the configured TLS listener ports for public mail clients and
verify the domain's records from outside your network.

### Automations

To run small event-driven logic, enable the language add-on you want, such as
JavaScript, Lua, Shell, Go, PHP, or HookScript. Then configure a Web route or
schedule to select that language and its source. Use the
[automation setup guide](automations.md) for language-specific limits,
schedule setup, and available actions. Do not expose Shell or PHP execution to
untrusted script authors.

### Managed realtime services

Gateway can manage Reverb, Mercure, and Centrifugo workloads through configured
deployment targets. Create a target, then create the application/server that
uses it. Choose Local, Docker, or Kubernetes only when that target is available
to Gateway. Follow [managed deployment targets](provisioning-targets.md) and
test the workload before sending users to it.

### Database

A Database add-on is reserved but is not currently shipped as a runnable
Gateway service. Do not plan a production database proxy or protocol gateway
around it yet.

## 6. Check and maintain your deployment

- Check GET /health for service availability. Verify delivery separately by
  checking the receiver and delivery records.
- Review failed callbacks at GET /deliveries?state=failed and check whether the
  receiver already processed the earlier attempt before retrying. Retries can
  send an event more than once.
- Keep the whole /data volume persistent. Back it up together with any
  separately mounted credentials and TLS files. For the simplest consistent
  backup, stop Gateway before copying its database and delivery state.
- Use one Gateway replica with the default local storage. Multiple replicas
  need shared database and cache configuration; see
  [Router deployment details](router.md#combined-router-docker-image).
- Pin production image and add-on versions. Review release notes, back up
  state, then pull and recreate the container while preserving its data volume.

## Security checklist

- Put Gateway behind a TLS endpoint with a real certificate.
- Publish only the ports needed by the features you enabled.
- Keep the management token private. Event senders and callback services do
  not need it.
- Never publish paths beginning with /_internal or the private delivery-admin
  and provisioning listeners.
- Configure callback destinations you control and inspect failures before
  retrying deliveries.

## Other setup guides

- [Gateway Router deployment](router.md)
- [Separate-service deployment](deployment.md)
- [Endpoint event history](events.md)
- [Delivery status and retries](deliveries.md)
- [Extension release and activation details](planner-extensions.md)
