# Outbound egress audit (2026-11-04)

This inventory classifies outbound calls in Gateway's Go hosts and built-in
Extensions. The source of a destination determines whether Gateway must apply
the shared public-address check, a narrower deployed network ceiling, or the
private Planner connection. A DNS lookup must be checked at the address used
for the socket, so validating a URL alone does not enforce the policy.

| Dial site | Destination authority and enforcement |
| --- | --- |
| `pkg/spinner/internal/forwarding/forwarder.go` | Subscription HTTP targets use `PublicEgressAddress` after resolution, at the socket dial. A private callback needs both a deployed ceiling and a narrower Planner CIDR grant; that path refuses HTTP proxies and redirects. Public transports may use endpoint proxy profiles; see proxy trust boundary below. |
| `pkg/web/spinner/source.go` | Subscription Webstream sources use `PublicEgressAddress` at dial. A private source needs a deployed ceiling and Planner grant, and refuses proxies. Neither source mode follows redirects. |
| `pkg/web/spinner/websocket/delivery.go` | Subscription WebSocket targets use `PublicEgressAddress` at dial or an explicit deployed private ceiling. The WebSocket dialer disables environment proxies. |
| `pkg/email/cmd/spinner/mailbox_poll.go` | Mailbox callbacks use `PublicEgressAddress` at dial or an Email Extension private callback ceiling. The transport disables proxies and redirects. |
| `pkg/email/spinner/mailnet/mail_upstream.go`, `pkg/email/spinner/{imap,pop3,smtpout}` | Planner-resolved mailbox/managed SMTP upstreams use `EgressAddressAllowed` at dial with their public/private scope and pin the checked IP. `smtpout.Sender` also retains an unscoped direct-dial path for the operator-configured relay in the Email Extension. |
| `pkg/email/spinner/delivery/hosted_mail_{managed,http}.go` | The operator configures hosted-adapter allowed hostnames and the private Planner-resolved adapter endpoint. HTTPS and no redirects are enforced; this is an operator-trusted destination, not a subscription URL. |
| `pkg/shell/runtime/ssh.go` | A Planner-resolved SSH hook supplies the host; the deployed SSH CIDR ceiling and pinned SSH host key authorize the connection. No ceiling disables the feature. `ssh_resolver.go` calls the private authenticated Planner endpoint. |
| `pkg/dns/spinner/{forwarded,resolver}.go`, `pkg/email/spinner/smtp/domain_existence.go` | Explicit Planner DNS forwarders or operator-selected recursive resolvers are infrastructure destinations. Client DNS query names do not choose a resolver address. UDP fallback to TCP keeps the same endpoint. |
| `pkg/spinner/provision/{remote_router,local_driver,control_plane,target}.go` and `pkg/spinner/cmd/provision/main.go` | The Planner origin and local probes are deployment infrastructure. Managed route targets from Planner are narrowed by `GATEWAY_PROVISIONING_ROUTE_CIDRS`; the route transport disables proxies. |
| `pkg/spinner/{proxy,router,schedules,bridge}/*.go`, `pkg/email/spinner/{plannerclient,delivery,smtpout}/*.go`, `pkg/caddy/caddy_handler.go`, `pkg/roadrunner/cmd/router/router.go` | Private Planner calls use the configured internal origin or an in-process handler with internal authentication. Their URL is not taken from a subscription. |
| `pkg/spinner/extensions/reconciler.go`, `pkg/spinner/extensions/integration/catalog/catalog.go` and Router artifact clients | Extension artifact URLs and integration catalogs come from operator-managed desired state or host installation. Downloads require HTTPS and verified package digests; Router artifact clients do not follow redirects, while catalog redirects remain HTTPS-only. |
| `pkg/email/spinner/smtp/clamd.go` | The malware scanner address is an operator-installed local Unix socket or private TCP service. SMTP message data cannot choose the scanner endpoint. |

## Proxy trust boundary

Endpoint HTTP proxy profiles and Webstream public proxy profiles deliberately
send requests through a configured proxy. Gateway validates the proxy socket
address, but a remote proxy resolves the final target itself. The Gateway-side
address check cannot prove what that proxy will dial. Treat permission to set
proxy profiles as permission to delegate outbound network policy to that proxy;
do not grant it to clients who should only control subscription targets.

## Private ceiling trust boundary

The HTTP, Webstream, WebSocket, mailbox callback, SSH, and managed-route private
ceilings are explicit deployment capabilities. A broad ceiling can include
loopback, link-local, or shared-use space, and the matching private paths do not
all apply `EgressAddressAllowed`. The owner who configures a ceiling must keep
it narrower than the host's sensitive networks. Planner grants can only narrow
the deployed ceiling where that second grant exists. WebSocket targets and
mailbox callbacks instead rely on their Extension ceiling directly; Shell
also requires Planner-resolved credentials and a pinned host key.

## Address registry maintenance

The shared Go policy and PHP mail credential probe now cover the additional
non-global blocks identified in the current IANA IPv4 and IPv6 special-purpose
registries, including the `2001::/23` IETF allocation with its globally
reachable exceptions. Both
implementations need the same future registry updates. A shared cross-language
conformance fixture is tracked separately from this audit.

- [IANA IPv4 special-purpose registry](https://www.iana.org/assignments/iana-ipv4-special-registry)
- [IANA IPv6 special-purpose registry](https://www.iana.org/assignments/iana-ipv6-special-registry)
