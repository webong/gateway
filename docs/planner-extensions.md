# Gateway Extension packages

Planner is Gateway's CMS kernel. It owns generic resources, authentication,
subscriptions, schedules, deliveries, extension distribution, and managed
server state. Spinner is Gateway's runtime host. Product behavior is
contributed by Gateway Extensions through plane-specific Planner and Spinner
entrypoints that share one package identity and version.

The first-party Extensions are self-contained package directories:

| Gateway Extension | Package | Planner entrypoint | Spinner entrypoint |
| --- | --- | --- | --- |
| Web | `webong/web` | Web capture policy and history views, WebSocket/Webstream policy, management routes | HTTP, WebSocket, and Webstream transport |
| DNS | `webong/dns` | Zones, Endpoints, Forwarders, provisioner policy, planning, management routes | Authoritative and policy-scoped forwarded UDP/TCP DNS service |
| Email | `webong/email` | Accounts, domains, routes, verification, delivery state, mail planning | SMTP, outbound mail, IMAP/POP3 retrieval, mail network policy |
| Database | `webong/database` | Reserved managed database policy | Reserved; advertises no capability until its contract exists |

Each package registers its Planner service provider through
`ExtensionProviderRegistry`. Planner discovers installed entrypoints and gives
them narrow registries for resource kinds, protocol planners, path resolvers,
route-plan observers, and native schedule jobs. Planner does not import the
Web, DNS, Email, or Database namespaces. The transport-neutral contract used by
both sides lives in Arranger; neither Extension needs to import Spinner core.
Planner owns the shared endpoint event ledger and retention lifecycle; Web and
DNS contribute protocol-specific recording and presentation behavior without
depending on each other.

Spinner entrypoints are verified `ext.package/v1` processes. A host reconciles
the complete package set selected by Planner, resolves exact `requires`
capabilities, launches providers before consumers, and exposes only the
selected reverse bindings from Arranger. The child never receives a Planner URL,
an internal token, or another package's process handle. Web advertises a private
loopback HTTP endpoint that Spinner proxies; DNS and Email open their configured
protocol listeners inside their verified child processes. Closing, replacing,
or failing integrity validation for a package closes that process and its
listeners.

Dependencies are capabilities rather than source dependencies. `webong/email`
declares `gateway.dns/v1 exchange`; `webong/dns` provides it. Both packages must
be active in Planner desired state. Spinner starts DNS before Email and pins
Email's calls to that exact DNS package revision. A missing, inactive,
ambiguous, cyclic, incompatible, or replaced provider prevents the dependent
package from starting. Resolver-only DNS configuration does not bind a public
authoritative port, so Email can use Gateway DNS without publishing a DNS
service.

`POST /extensions` and `PUT /extensions/{id}` accept an optional
`configuration` object alongside the pinned artifact. Planner stores it in the
generic, revisioned Extension resource and includes it in the private desired
state. A Planner entrypoint may project its own typed resources into that
snapshot; for example, `webong/dns` injects the active `/dns/zones` catalog, so
operators do not maintain a duplicate zone list. Spinner verifies the projected
configuration digest and passes the object only to the selected package
revision; the Extension owns its schema and validation. Configuration is
bounded to 64 KiB and is not a secret store. Package revisions, configuration
changes, and projected-resource changes
restart the runtime, while missing, malformed, inactive, ambiguous, or
integrity-invalid providers fail closed.

The bundled Planner host requires all four packages. A separately assembled
Laravel Planner and Spinner may install only the Gateway Extensions they need.
Installing Composer code remains a Planner deployment/build-time operation.
Spinner Go entrypoints are installable after the host starts through the
Planner-owned artifact desired state; installing one does not rebuild Spinner.
Planner's `/extensions` API does not rewrite a running PHP host's Composer
installation.

For example, DNS listener and zone state travels with the DNS Extension:

```json
{
  "extension": "webong/dns",
  "enabled": true,
  "version": "1.0.0",
  "artifact": {
    "url": "https://extensions.example.com/webong-dns-1.0.0.tar.gz",
    "sha256": "<archive-sha256>"
  },
  "configuration": {
    "address": ":5353",
    "planner_timeout_ms": 750,
    "read_timeout_ms": 2000,
    "write_timeout_ms": 2000,
    "max_response_records": 16,
    "queries_per_second": 5000,
    "query_burst": 10000
  }
}
```

Create and update authoritative zones through `/dns/zones`; the DNS Planner
entrypoint projects them into the runtime snapshot above.

Email listener state is carried by the Email Extension and depends on DNS:

```json
{
  "extension": "webong/email",
  "enabled": true,
  "version": "1.0.0",
  "artifact": {
    "url": "https://extensions.example.com/webong-email-1.0.0.tar.gz",
    "sha256": "<archive-sha256>"
  },
  "configuration": {
    "smtp": {
      "address": ":2525",
      "hostname": "mail.example.com",
      "max_message_size": 10485760
    }
  }
}
```

The DNS Extension may omit its public `address`, zones, and forwarders and run
only the private resolver capability. Set `resolver_address` to an explicit IP
and port when the host's `/etc/resolv.conf` should not be used. Legacy
`GATEWAY_SMTP_ADDR`, `GATEWAY_IMAP_ADDR`, and `GATEWAY_POP3_ADDR` listener
configuration is rejected by Spinner hosts; those listeners now belong to the
revisioned `webong/email` configuration.

The same package can be built locally with:

```sh
scripts/build-gateway-runtime-extension pkg/dns /tmp/extensions/webong%2fdns 1.0.0
scripts/build-gateway-runtime-extension pkg/email /tmp/extensions/webong%2femail 1.0.0
```
