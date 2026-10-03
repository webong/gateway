# Authoritative DNS across two Spinner nodes

Gateway's DNS listener is authoritative-only. Planner owns DNS hook identities,
decisions, and event history; each Spinner answers UDP and TCP queries using
the same configured zones and its private Planner connection. DNS is not a
recursive resolver.

## Deployment shape

Run `docker-compose.dns.example.yml` once on **each of two separate hosts or
failure domains**. Give each host its own public address and persistent
process supervision. Both must run the same Gateway release and receive the
same `GATEWAY_DNS_ZONES`, `GATEWAY_INTERNAL_TOKEN`, and private
`GATEWAY_LARAVEL_BACKEND_URL`. Do not expose Planner publicly. The Compose
example publishes both `53/udp` and `53/tcp` on its host.

For example, both Planner and Spinner nodes might receive:

```sh
GATEWAY_DNS_ZONES='[{"name":"hooks.example.com","nameservers":["ns1.example.net","ns2.example.net"],"ttl":300}]'
```

Publish the nameserver address records, then delegate `hooks.example.com` to
both nameserver names at its parent zone. If a nameserver name is inside the
delegated zone, arrange the required parent-side glue as well. Use distinct
hosts and network paths for real availability; two containers on one host
only test process redundancy. Gateway does not create registrar or parent-zone
records for you.

## Verify before routing traffic

Replace the placeholders with the actual zone, public node addresses, and a
parent-zone authoritative server. Check the delegation and query each node
directly over both transports:

```sh
go run -mod=mod ./src/spinner/cmd/dns-delegation-check \
  -zone hooks.example.com \
  -parent PARENT_NS_IP \
  -parent-zone example.com \
  -resolver PUBLIC_RECURSIVE_RESOLVER_IP \
  -nodes NS1_PUBLIC_IP,NS2_PUBLIC_IP \
  -nameservers ns1.example.net,ns2.example.net \
  -probe hello.REGISTERED_TOKEN.hooks.example.com \
  -negative-probe hello.UNREGISTERED_TOKEN.hooks.example.com \
  -probe-type TXT
```

This read-only check requires numeric public server IPs. The `-nodes` and
`-nameservers` lists must be in matching order: each nameserver must resolve
through the supplied recursive resolver to its corresponding node IP. It
requires an authoritative SOA from the specified parent zone and compares
that parent's non-authoritative delegation referral with the expected NS set.
It then requires both nodes to answer authoritative NS and SOA queries, a
registered hook with `NOERROR` (an answer or valid NODATA), and the known
unregistered control name with `NXDOMAIN`, all over UDP and TCP. The control
name must really be unregistered and use the same query shape as the probe.
It also checks that the two nodes return the same SOA. Use `-allow-private`
only for a disposable local test; it is not evidence of public reachability.
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
same NS set, and return a consistent SOA. Also query a real registered hook
through each node over UDP and TCP. Then stop one Spinner and repeat the
direct queries against the surviving node. Finally, query through ordinary
recursive resolvers from more than one network. A local direct-answer test
does not prove that public delegation, firewalls, or resolver paths work.

`make test-dns-integration` runs a disposable local analogue: two configured
zones, two independent Spinner processes, shared Planner hook state, UDP and
TCP queries on each process, and continued answers after one Spinner stops.
It also runs the deployment checker against a synthetic parent referral and
recursive address view while both Spinners are live. It does not modify public
DNS or prove cross-host availability or external resolver propagation.

For Kubernetes, the optional [Spinner DNS overlay](kubernetes.md#authoritative-dns-overlay)
serves both protocols through a ClusterIP Service. `scripts/test-kubernetes-dns`
tests that overlay on OrbStack using temporary UDP and TCP NodePorts, two zones,
and a Spinner Pod replacement. This is still one-node process resilience, not
proof of public delegation or independent host and network failure domains.
