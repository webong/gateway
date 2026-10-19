# Planner schema direction

Planner is Gateway's CMS for operator-managed desired state. A resource kind supplies validation, authorization, and a runtime projection; `configurations` supplies stable identity, encrypted documents, enablement, revision checks, and audit history. The generic store is not an arbitrary key/value API. Process bootstrap settings and hard deployment ceilings are not Planner resources.

An operational record is different: it is produced while Gateway handles traffic or runs work. Atomic claims, uniqueness, retention, and indexed lookup matter more than revision history. Operational records may use shared tables when their access patterns actually match, but should not be forced into `configurations`.

| Concern | Current storage | Direction |
| --- | --- | --- |
| Webstream, Web, Shell, Email policies, upstreams, hosted routes, DKIM keys | `configurations`, `configuration_revisions`, `configuration_secrets` | Keep typed resources; live credentials stay out of revisions. |
| Email domains and aliases | `email.domain` and child `email.alias` resources | Done; no dedicated definition tables. |
| Account and sender-domain submission quotas | `usage_counters`, scoped by metric and subject hash | Done; one atomic counter table, not two Email-specific tables. |
| Accounts and mailbox connections | Generic `accounts` principals created through `POST /accounts`; account-owned `email.mailbox-connection` resources | Done: definitions and audit are shared, live credentials are in `configuration_secrets`, and test health is in `resource_observations`. `configurations.owner` references `accounts.id`. The old Mail account-create path remains an alias for local callers. |
| Email suppressions | `email_suppressions` | Move operator-managed entries to typed resources only after preserving fast membership checks and stable cursor pagination with an indexed key projection. |
| Schedules | `schedules`, `schedule_runs`, `schedule_notifications` | Move schedule definitions to resources. Keep due-time/claim state and runs/notification outbox as operational records. |
| Managed servers, applications, deployment targets | Dedicated definition tables | Move desired state to typed resources after defining parent, owner, and unique-route indexes. Do not move observed instances or node heartbeats into resource documents. |
| Events, deliveries, SRS returns, HTTP submission idempotency, admin endpoint leases, cache | Purpose-built operational tables | Keep transactional/retention semantics. Consider reusable tables only where identity, expiry, and claim behavior are truly the same. |

The local-only schema is allowed to reset; the baseline migration history can be rewritten. Do not reset a developer's existing database implicitly, and do not treat the previously published ARM64 mail image as a build of the evolving schema in this checkout.
