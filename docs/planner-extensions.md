# Gateway Extensions in Planner

Planner is Gateway's CMS kernel. It owns generic resources, authentication,
subscriptions, schedules, deliveries, extension distribution, and managed
server state. Product behavior is contributed by Gateway Extensions through a
Planner entrypoint.

The first-party Planner entrypoints are separate Composer packages:

| Gateway Extension | Planner package | Owns |
| --- | --- | --- |
| Web | `webong/web` | Webhook event history, WebSocket and Webstream policy, and Web management routes |
| DNS | `webong/dns` | Zones, DNS hooks, provider policy, DNS planning, and DNS management routes |
| Email | `webong/email` | Mail accounts and routes, verification, submission, delivery status, mailbox polling, and mail planning |
| Database | `webong/database` | Reserved for managed database bindings; it advertises no capability until that contract exists |

Each package registers its service provider through
`ExtensionProviderRegistry`. Planner discovers installed entrypoints and gives
them narrow registries for resource kinds, protocol planners, path resolvers,
route-plan observers, and native schedule jobs. Planner does not import the
Web, DNS, Email, or Database namespaces.

The bundled Planner host requires all four packages. A separately assembled
Laravel Planner may require only the Gateway Extensions it needs. Installing a
Composer package is a deployment-time operation; Planner's `/extensions` API
still controls desired state and distributed extension artifacts, but it does
not rewrite a running PHP host's Composer installation.
