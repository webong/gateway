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

Each package registers its service provider through
`ExtensionProviderRegistry`. Planner discovers installed entrypoints and gives
them narrow registries for resource kinds, protocol planners, path resolvers,
route-plan observers, and native schedule jobs. Planner does not import the
Web, DNS, Email, or Database namespaces. The transport-neutral contract used by
both sides lives in Arranger; neither Extension needs to import Spinner core.

The bundled Planner host requires all four packages. A separately assembled
Laravel Planner and Spinner may install only the Gateway Extensions they need.
Installing Composer or Go code remains a deployment/build-time operation;
Planner's `/extensions` API controls desired state and distributed extension
artifacts, but it does not rewrite a running PHP host's Composer installation.
