---
status: accepted
---

# Resolve Extension secrets through Planner

Gateway Extension configuration is revisioned desired state and must not carry
credentials. Planner instead owns encrypted, non-revisioned secret documents
under the same generic Extension activation resource. Operators write a named
secret through `/extensions/{activation-id}/secrets/{reference}` and put only
that reference in typed Extension configuration or another Planner resource.

## Contract

- A reference is an opaque lower-case name. The value is a bounded JSON object
  whose schema belongs to the consuming provider.
- Public CMS reads expose reference names and versions, never values. Creation,
  rotation, and deletion require registry authentication and HTTPS; replacement
  and deletion additionally require the current version in `If-Match`.
- Values are encrypted in `configuration_secrets`, excluded from configuration
  revisions and Extension desired-state distribution, and deleted with their
  owning activation.
- A verified Spinner Extension may declare `gateway.secret/v1 resolve`. The
  host binds the call to that process's verified identity and resolves only
  `/_internal/gateway/extensions/{identity}/secrets/{reference}` through its
  authenticated private Planner connection. The child receives no Planner URL
  or internal token.
- Planner-side Extension code resolves through the same store using its fixed
  package identity. Missing, disabled, malformed, cross-identity, or stale
  values fail closed.
- Providers resolve immediately before use. Rotation therefore does not require
  a Spinner restart or republishing an Extension artifact.

## Provider ownership

| Provider surface | Secret ownership |
| --- | --- |
| Email direct SMTP relay | `webong/email` secret reference; resolved for every delivery attempt. |
| Email managed SMTP, mailbox SMTP, hosted mail, DKIM | Typed Email resources keep their existing encrypted credentials; the Email runtime resolves them through identity-scoped private Planner routes. |
| DNS Cloudflare provisioner | `webong/dns` secret reference containing `api_token`; zone desired state stores only `credential_ref`. |
| Web transport and DNS query runtime | No provider credential today. Future authenticated upstreams must use a secret reference, not runtime configuration. |
| HookScript, JavaScript, Lua, PHP, and Shell | Executors are not granted an arbitrary secret catalog. Invocation-scoped credential data and Shell's typed SSH credential resource retain their narrower contracts. |
| Reverb, Mercure, and Centrifugo workloads | Their application/server credentials remain encrypted typed managed-workload state. They are provisioned workloads, not Spinner Extension runtime configuration. |
| Database | No runtime capability or provider credential exists yet. |

Deployment credentials that establish Gateway itself, such as the database
connection, Planner internal authentication, and OAuth application registration,
remain deployment secrets. Listener availability and hard network/safety ceilings
also remain deployment policy; Planner may select behavior only within them.

## Consequences

The generic host gains one small identity-scoped secret-resolution contract
instead of product-specific credential environment variables. Product packages
remain responsible for validating exact credential shapes and for preserving
more specific resource contracts where they provide stronger authorization or
lifecycle semantics. A generic secret reference is not an invitation to expose
all stored credentials to Automations or Companions.
