# Configuration ownership audit

Gateway has two config owners. Planner stores **desired product state** that an
operator can create, inspect, change, and disable at runtime. Deployment
configuration defines the **capabilities and hard limits** of a particular
Router, Spinner, or Planner process: listeners, private connectivity, secrets,
installed binaries, and maximum resource or network access. A Planner resource
can narrow a deployed capability; it cannot open an undeployed listener or
expand a network safety ceiling.

Planner is a CMS for this desired state, not a separate SQL table for every
configuration kind. Typed resources share the encrypted, versioned
`configurations` collection. A kind-specific module validates the document and
projects it into decisions or runtime snapshots; the shared store owns identity,
revision, encrypted change history, conditional writes, persistence, and
cross-kind isolation. `GET /configurations/kinds` lists registered kinds and
`GET /configurations/{kind}/{id}/revisions` exposes metadata-only history to
authenticated registry operators. Typed APIs return ETags; updates and deletes
require `If-Match` and reject stale revisions. The Webstream private-source
and route policies are the first two kinds on this model. Existing domain
records such as schedules, servers, and subscriptions are not automatically
flattened into configuration documents: they have their own transactional
lifecycles and relationships.

This audit covers active settings in `pkg/planner/config`, Spinner's config
loader, and provisioning. Test and smoke variables are not product settings.

For a fresh local setup, create the operator policies after migrations:

```http
POST /email/runtime-policy
Authorization: Bearer <REGISTRY_TOKEN>
Content-Type: application/json

{"http_submission_enabled":true,"connected_smtp_enabled":true,"dmarc_enforce_enabled":true,"verification_ttl_seconds":86400,"submission_usage_retention_days":90}
```

```http
POST /web/event-policy
Authorization: Bearer <REGISTRY_TOKEN>
Content-Type: application/json

{"enabled":true,"retention_days":30}
```

Both use the shared `configurations` table and return ETags. Replace with a
current `If-Match` header. Delete returns to defaults; disabling stops the
opt-in behavior. Mail hooks need no global switch: only an active hook on a verified
domain can receive mail. These are policy changes, not listener provisioning.

| Surface | Planner-owned state now | Deployment-owned settings | Gap |
| --- | --- | --- | --- |
| Webhook/WebSocket | Endpoints, subscriptions, routes, targets, event retention through `/web/event-policy`, body capture opt-in per endpoint, and WebSocket limits in the `webong/web` runtime snapshot | Public HTTP listener, global body ceiling, authentication keys | Event history is off until the operator creates an enabled policy. |
| Webstream | Subscription-selected source; private exact-origin source/CIDR grants via `/webstream/private-sources`; upload/retry policies via `/webstream/routes`; network ceiling, heartbeat, retry, and connection/duration limits in `webong/web` runtime configuration | Public HTTP listener and global body ceiling | Generic hosts no longer read Webstream settings. Per-request propagation and host/path isolation across two Spinners are verified by `scripts/config-ownership-smoke.sh`. |
| DNS | Zones, nameservers, SOA, TTL, provided Endpoints, Forwarders, and the installable runtime snapshot through `/extensions` | Provisioner credentials and host/network permission to bind the selected address | Spinner no longer parses DNS environment variables or imports DNS. The selected `webong/dns` process validates its bounded listener configuration and revisioned desired-state snapshot before serving. |
| Email | Domains, aliases, suppressions, hooks, account-owned mailbox connections, submission quotas, runtime behavior through `/email/runtime-policy`, SRS policy, upstream catalogs, hosted adapters/routes, DKIM keys, and the revisioned `webong/email` listener/relay/failure-follow-up snapshot | Port publication, referenced TLS/scanner secret files, OAuth application credentials, and private Planner connectivity | Planner selects mailbox endpoints; the Email process validates and pins DNS answers before dialing. Generic hosts neither import Email nor parse Email settings. |
| Schedules | Schedule definitions/runs and a secret-backed failure-notification destination through `/schedules/notification-destination` | Worker activation, poll interval, process limits, exact allowed alert hosts | Former notification URL/token env settings are no longer read. Revision rotation, stale-write rejection, host ceiling, and token secrecy are verified by `scripts/config-ownership-smoke.sh`. |
| Managed workloads | Targets, servers, applications, instances, desired state | Worker credentials, integration paths, Docker/Kubernetes host access, images, private route network ceiling | Route target selection already comes from Planner. `GATEWAY_PROVISIONING_ROUTE_CIDRS` and Caddy `managed_route_cidrs` are SSRF ceilings, not desired route state; retain them in deployment config. A future per-target route policy may narrow these ceilings but must never replace them. |
| Shell/automation | Subscription selection, schedules, exact local/SSH hook grants through `/shell/hook-grants`, and write-only SSH client credentials | Hook files, installed language engines, separate mutation token, SSH outbound CIDR ceiling | Static hook-grant config is no longer read. Planner grants cannot install or widen deployed hook capability; SSH keys are kept out of revision history. Local grants are verified across two Spinners by `scripts/config-ownership-smoke.sh`; the SSH path by `scripts/shell-ssh-smoke.sh`. |

Other env settings such as cache stores/TTLs, table names, internal URLs and
tokens, delivery spool/retry limits, and generic host timeouts are process
or storage controls. They should not become a generic API
for writing env keys. A changed process-level setting may still require a
rollout. Conversely, changing a Planner resource should be observable without
restarting the Spinner host; a changed Extension snapshot restarts only that
verified Extension process.

## Verification

Three disposable, root-free smoke harnesses exercise the ownership boundary on a
local multi-Spinner deployment:

- `scripts/config-ownership-smoke.sh` starts one Planner fixture and two Spinner
  nodes that share it and asserts that:
  - an already-running Spinner pair honors a Shell local hook grant created after
    they started and stops executing once it is revoked, without a redeploy;
  - Webstream private-source grants and route policies appear in the per-request
    trusted decision and stay isolated to their exact origin and host/path;
  - the schedule-notification singleton rotates through revisions, rejects stale
    writes, refuses an undeployed host, and never exposes its token;
  - Mail upstreams, DKIM keys, hosted adapters, and mailbox connections resolve
    through the private per-attempt endpoints, reflect rotation and revocation,
    gate enablement on provider health, and keep every secret encrypted at rest.
- `scripts/shell-ssh-smoke.sh` runs the full Shell-over-SSH path against a
  disposable OpenSSH forced-command server. `make test-dns-integration` builds
  and installs the DNS process package after Spinner starts and exercises the
  focused Planner CMS contracts.

## Remaining work, in order

1. The installable Email package's direct `smtp` adapter is covered end to end
   by the production-like Docker smoke, including Planner submission, its
   authenticated private Extension mount, durable receipt/replay, Spinner
   restart, relay acceptance, and failure follow-up. `managed.smtp`,
   `mailbox.smtp`, and `hosted.mail` still need the same installable-runtime
   wiring plus identity-scoped private Planner lookups before their legacy host
   implementations can be removed. Provider health is still observed through
   the mailbox health-state machine rather than a real healthy upstream. Keep
   OAuth app registration and listener/TLS bootstrap in deployment config.
2. Cross-node dispatch for schedule notifications (exactly one alert through the
   DB claim/lease), cross-kind isolation at the smoke level, and rollback remain
   only partly covered. Rollback is not a first-class operation: resources are
   re-PUT with a prior revision, and DNS zones have no update or revert API.
