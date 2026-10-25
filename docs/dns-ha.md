# Authoritative DNS across two Spinner nodes

Gateway's DNS Extension is authoritative for Managed and Provided zones. Planner owns DNS Endpoint identities,
decisions, and event history; each Spinner answers UDP and TCP queries using
the same revisioned Extension snapshot. Forwarders may delegate explicitly
scoped queries to an upstream resolver; Gateway does not implement recursion.

## Deployment shape

Run a Spinner on **each of two separate hosts or failure domains**. Give each
host its own public address and persistent process supervision. Planner owns
the pinned `webong/dns` artifact and desired state; each Spinner independently
pulls and verifies the package, then launches only its Go entrypoint with the
projected snapshot. The Extension process never receives Planner credentials.
Publish both `53/udp` and `53/tcp` on each host.

Create each zone through `POST /dns/zones`, including its `nameservers` array.
Zone, nameserver, SOA, TTL, and Forwarder state are not environment settings.
There is no production installation to migrate; configure a fresh local
deployment through Planner before testing delegated DNS.

The operator may then replace the zone's complete static answer set through
`PUT /dns/zones/{zone}/records` with the current `ETag` in `If-Match` and
`{"records":[...]}`. It accepts exact
owners inside the zone and `A`, `AAAA`, `CNAME`, `MX`, and `TXT` records;
Gateway manages the zone's `NS` and `SOA` itself. For example, to have
Gateway answer the MX query for `support@sosongo.com`, the zone would need
an apex record such as:

```json
{"records":[{"name":"sosongo.com","type":"MX","value":"route1.mx.cloudflare.net","preference":10}]}
```

The real MX targets and priorities must come from the receiving provider's
current configuration. Include every other needed record (website addresses,
SPF, DKIM, DMARC, verification, sending-provider records) before moving a
whole zone's authority. A `PUT` is a complete replacement, not a patch.
Planner snapshot revisions replace the DNS Extension process; an invalid
snapshot fails closed. Static records answer exact names before
dynamic token-based DNS Endpoints, and a present name without a requested record
type returns NODATA rather than NXDOMAIN.

There are two operator choices. Delegate a subdomain such as
`mail.sosongo.com` from the Cloudflare-hosted parent to Gateway; then Gateway
can answer that subdomain's MX, but the mail address is
`support@mail.sosongo.com`. To make Gateway answer the MX for
`support@sosongo.com`, the registrar must delegate the entire `sosongo.com`
zone to Gateway's public nameservers. An NS record on `mail.sosongo.com`
cannot move the parent apex MX lookup. Do not change the registrar or parent
NS records until two independently reachable Gateway DNS nodes answer the
complete zone correctly over public UDP and TCP port 53 and the DNS records
have been audited. Cloudflare Tunnel's HTTPS path does not expose DNS on
port 53. This repository supplies the authoritative server and verification
checker, not public hosts or an automatic registrar cutover.
This is a DNS capability, not a promise that every mail provider supports
external authoritative DNS. In particular, Cloudflare's Email Routing setup
currently requires Cloudflare DNS for the routed domain. Keep `sosongo.com`
on Cloudflare for the planned Cloudflare Email Worker test; moving its apex
authority to Gateway would require a different receiving arrangement or
provider-confirmed compatibility. The static Cloudflare MX values above are
illustrative only and must not be used as a cutover plan by themselves.

Publish the nameserver address records, then delegate `hooks.example.com` to
both nameserver names at its parent zone. If a nameserver name is inside the
delegated zone, arrange the required parent-side glue as well. Use distinct
hosts and network paths for real availability; two containers on one host
only test process redundancy. Gateway does not create registrar or parent-zone
records for `provisioner=manual`. The Cloudflare provisioner can create child-zone NS
records in a managed parent; apex registrar nameserver changes require a
registrar-specific adapter or manual configuration.

## Verify before routing traffic

Replace the placeholders with the actual zone, public node addresses, and a
parent-zone authoritative server. Check the delegation and query each node
directly over both transports:

```sh
GOWORK=off go -C tools run -mod=mod ./dns-delegation-check \
  -zone hooks.example.com \
  -parent PARENT_NS_IP \
  -parent-zone example.com \
  -resolver PUBLIC_RECURSIVE_RESOLVER_IP \
  -nodes NS1_PUBLIC_IP,NS2_PUBLIC_IP \
  -nameservers ns1.example.net,ns2.example.net \
  -probe hello.REGISTERED_TOKEN.hooks.example.com \
  -negative-probe hello.UNREGISTERED_TOKEN.hooks.example.com \
  -probe-type TXT \
  -expect-mx 10:route1.mx.cloudflare.net
```

This read-only check requires numeric public server IPs. The `-nodes` and
`-nameservers` lists must be in matching order: each nameserver must resolve
through the supplied recursive resolver to its corresponding node IP. It
requires an authoritative SOA from the specified parent zone and compares
that parent's non-authoritative delegation referral with the expected NS set.
It then requires both nodes to answer authoritative NS and SOA queries, a
registered Endpoint with `NOERROR` (an answer or valid NODATA), and the known
unregistered control name with `NXDOMAIN`, all over UDP and TCP. The control
name must really be unregistered and use the same query shape as the probe.
It also checks that the two nodes return the same SOA. When `-expect-mx` is
supplied, it additionally compares the exact apex MX
answers on both nodes over UDP and TCP. Multiple exchanges use a
comma-separated `priority:exchange` list. Use `-allow-private` only for a
disposable local test; it is not evidence of public reachability.
One resolver's view is not proof of global propagation or correct parent glue;
query from other networks before accepting traffic.

You can run equivalent manual queries to inspect the raw records and flags:

```sh
dig @PARENT_NS example.com SOA +norecurse +noall +comments +answer
dig @PARENT_NS hooks.example.com NS +norecurse +noall +answer
dig @NS1_IP hooks.example.com NS +norecurse +noall +comments +answer
dig @NS2_IP hooks.example.com NS +norecurse +noall +comments +answer
dig @NS1_IP hooks.example.com SOA +norecurse +noall +comments +answer
dig +tcp @NS2_IP hooks.example.com SOA +norecurse +noall +comments +answer
```

Both direct answers must be authoritative (`aa` in the `dig` flags), list the
same NS set, and return a consistent SOA. Also query a real registered Endpoint
through each node over UDP and TCP. Then stop one Spinner and repeat the
direct queries against the surviving node. Finally, query through ordinary
recursive resolvers from more than one network. A local direct-answer test
does not prove that public delegation, firewalls, or resolver paths work.

`make test-dns-integration` verifies the installable DNS process boundary and
the focused Planner CMS contracts. It does not simulate two public nodes,
modify public DNS, or prove cross-host availability or external resolver
propagation. Use the checker above against the real deployment before routing
traffic.

For Kubernetes, the optional [Spinner DNS overlay](kubernetes.md#authoritative-dns-overlay)
serves both protocols through a ClusterIP Service. `scripts/test-kubernetes-dns`
tests that overlay on OrbStack using a caller-supplied HTTPS DNS Extension
artifact, temporary UDP and TCP NodePorts, two zones, and a Spinner Pod
replacement. This is still one-node process resilience, not
proof of public delegation or independent host and network failure domains.
