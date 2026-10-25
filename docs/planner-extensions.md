# Gateway Extension packages

Planner is Gateway's CMS kernel. It owns generic resources, authentication,
subscriptions, schedules, deliveries, extension distribution, and managed
server state. Spinner is Gateway's runtime host. Product behavior is
contributed by Gateway Extensions through plane-specific Planner and Spinner
entrypoints that share one package identity and version.

The first-party Extensions are self-contained package directories:

| Gateway Extension | Package | Planner entrypoint | Spinner entrypoint |
| --- | --- | --- | --- |
| Web | `webong/web` | Webhook history, WebSocket/Webstream policy, management routes | HTTP, WebSocket, and Webstream transport |
| DNS | `webong/dns` | Zones, hooks, provider policy, planning, management routes | Authoritative UDP/TCP DNS service |
| Email | `webong/email` | Accounts, domains, routes, verification, delivery state, mail planning | SMTP, outbound mail, IMAP/POP3 retrieval, mail network policy |
| Database | `webong/database` | Reserved managed database policy | Reserved; advertises no capability until its contract exists |

Each package registers its Planner service provider through
`ExtensionProviderRegistry`. Planner discovers installed entrypoints and gives
them narrow registries for resource kinds, protocol planners, path resolvers,
route-plan observers, and native schedule jobs. Planner does not import the
Web, DNS, Email, or Database namespaces. The transport-neutral contract used by
both sides lives in Arranger; neither Extension needs to import Spinner core.

Spinner entrypoints are verified `ext.package/v1` processes. A host reconciles
the package selected by Planner, launches only its named `spinner` entrypoint,
and exposes narrowly declared reverse capabilities from Arranger. The child
never receives a Planner URL or internal token. Web currently advertises a
private loopback HTTP endpoint that Spinner proxies; DNS opens its configured
UDP/TCP listener inside the verified child. Closing, replacing, or failing
integrity validation for a package closes that process and its listeners.

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

The same package can be built locally with:

```sh
scripts/build-gateway-runtime-extension pkg/dns /tmp/extensions/webong%2fdns 1.0.0
```
