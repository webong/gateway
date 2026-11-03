# Gateway configuration ownership audit

Gateway should use Planner as the source of truth for operator-managed service configuration. The shared `configurations` collection provides typed, revisioned resources; each package owns its resource validation and runtime projection. This is not an arbitrary environment-variable or JSON key/value API.

## Configuration sources

| Source | Owns | Examples |
| --- | --- | --- |
| Planner resources | Mutable service intent and policy; values that operators create, change, audit, or disable after startup | DNS zones and forwarders, Web and Webstream policies, Email routes and upstreams, extension activation/configuration, schedules, managed server configuration |
| Host bootstrap file | Process and platform settings needed before Planner is reachable | Bind addresses, database connection, internal credentials, TLS paths, installed binaries, integration installation paths, storage directories |
| Environment | Bootstrap-file selection, secret injection, container/platform overrides, and temporary compatibility | Database password, app encryption key, Planner URL, bind port |
| Hard safety ceilings | Non-overridable limits enforced by the running host | Maximum request body, automation deadline/output ceiling, maximum worker and queue bounds, permitted image registries or network ranges |

The Planner API uses JSON today. YAML can be an operator-facing import/export format that is parsed into the same typed Planner resources. It must not become a second live source of truth. Gateway currently has no general YAML/JSON bootstrap loader; `GATEWAY_ENV_FILE` only reads a simple dotenv file for Spinner automation package directories.

## Current environment-driven product settings to migrate

| Area | Current environment-driven settings | Planner destination |
| --- | --- | --- |
| Centrifugo, Mercure, Reverb | Docker/Kubernetes image and container port; feature `*_ENABLED` toggles | Each existing `/servers` resource's typed `configuration.runtime` (`image`, `container_port`). Installed integrations advertise capability; an env toggle should not activate code. |
| Email | Google/Microsoft OAuth client settings, callback/upstream IDs, global policy, shared sender, managed DKIM/SPF settings, and authenticator/provider class selectors | Typed Email resources and generic secret references. Class names are package registration and stay host code; credentials must be stored as secrets, never in revision documents. |
| Shell | Operator token, SSH allowed CIDRs, output mode/interpreter and run-log policy | Shell-owned Planner resources and secret references. Executable paths and absolute resource roots remain host capabilities. Planner values must be bounded by host ceilings. |
| Schedules | Notification allowed-host policy and delivery destination | Existing typed `schedule.notification` resource. Keep host-side egress ceilings separate from the editable destination policy. |
| Spinner automation | Configured language list | Derive advertised languages from installed, verified guest capabilities; do not duplicate package inventory in an env list. |
| Spinner routing/provisioning | HTTP private-network policy and provisioning route CIDRs | Planner policy for requested routes/source grants, checked against host-enforced network ceilings. |

## Host settings that remain outside Planner

Planner cannot configure a process before it starts or create a capability the host does not have. Keep database/app encryption credentials, internal Planner/Spinner authentication, bind/listener addresses, TLS material paths, local executable and CLI paths, installed integration/guest roots, node identity bootstrap, durable spool locations, and hard safety ceilings in deployment configuration. Where these currently use environment variables, a YAML/JSON host file can improve operator ergonomics without making them Planner-owned.

Smoke-test, benchmark, and build variables (`*_SMOKE_*`, `GATEWAY_TEST_*`, `GATEWAY_BENCH_*`, image build tags) are test/build controls, not runtime product configuration.

## Recommended migration order

1. Move image and container-port selection for Centrifugo, Mercure, and Reverb into each server's existing encrypted Planner configuration. Remove the redundant per-integration enable toggles. Keep executable/CLI availability as host capability.
2. Move Email OAuth/provider and global mail policy settings into Email-owned typed resources, using the existing shared secrets collection for credentials.
3. Move Shell runtime policy and operator credentials into Shell-owned resources/secrets; keep process paths and hard execution limits on Spinner.
4. Project remaining shared Spinner policy from Planner to Spinner through a versioned private runtime snapshot. Spinner validates it against deployment ceilings and fails closed on unsupported or stale configuration.
5. Add one typed YAML/JSON bootstrap loader for the remaining host-only settings, with environment variables as secret injection and temporary overrides. Do not translate arbitrary `GATEWAY_*` keys into Planner documents.

No migration or code changes are performed by this audit document. Existing package APIs, generic storage, and host bootstrap remain unchanged until each migration is implemented with its package-owned validation and runtime contract.
