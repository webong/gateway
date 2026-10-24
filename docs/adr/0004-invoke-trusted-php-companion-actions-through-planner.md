---
status: accepted
---

# Invoke trusted PHP companion actions through the Planner host

A Gateway extension may pair its Spinner implementation with a trusted PHP Companion Action that uses the already-running Planner application's services. The PHP code is installed and registered once as part of an operator-approved, versioned companion package; Spinner invokes it by stable action name with typed input and receives a structured result. Spinner does not upload raw PHP through Planner's public API or resend source for each execution. This keeps the contract independent of FrankenPHP, RoadRunner, PHP-FPM, or an HTTP-hosted Planner while allowing one extension to contribute behavior to both Gateway planes.

## Contract

- The companion package owns its PHP source, dependencies, autoload rules, provider registration, action names, and input/output schemas.
- Planner's private Action Runner resolves only registered actions and authenticates Spinner using the existing internal trust boundary. The transport adapter may be in-process or private HTTP, but it carries the same versioned invocation contract.
- An invocation contains an action name, companion version or digest, execution ID, deadline, idempotency key, and size-bounded typed input. Its result is structured data or a declared failure; PHP output is never treated as an implicit protocol.
- Installation, upgrade, rollback, and removal happen through deployment and package lifecycle. A long-running PHP host is reloaded when its installed companion set changes.
- Planner advertises registered action capabilities and versions so Spinner fails closed rather than invoking an absent or incompatible companion.

## Trust boundary

PHP Companion Actions are trusted deployment code and run with the privileges of the Planner application, including any services deliberately injected into them. They are not a sandbox for tenant-authored or request-supplied PHP. Operator-authored Automations continue to use the explicitly installed, capability-bounded scripting engines; untrusted PHP must run in a separately isolated runtime rather than inside Planner.

Planner's public CMS API may configure a route to a registered action, but it cannot create an action by accepting PHP source. The private Action Runner accepts invocation data only. Companion actions receive a narrow context and declared services instead of unrestricted access to Planner internals where practical.

## Considered options

- Send raw PHP source from Spinner for every execution. Rejected because it turns the Planner boundary into a remote-code-execution API, repeats code transfer, complicates provenance and caching, and risks state leakage in long-running PHP workers.
- Execute companion PHP directly inside Spinner through an embedded FrankenPHP engine. Retained for explicitly installed PHP Automation runtimes, but rejected as the companion-action contract because it couples extension behavior to one PHP host and does not run inside the provided application's container and service graph.
- Invoke an application-owned HTTP endpoint without a Gateway action contract. Supported as an ordinary delivery, but insufficient for installed companions because capability discovery, version compatibility, idempotency, deadlines, and structured failures would be application-specific.

## Consequences

Gateway needs a transport-neutral Action Runner contract, Planner-side action registry and capability advertisement, Spinner invocation client, and lifecycle tests covering missing or mismatched companions, timeouts, retries, duplicate execution IDs, worker reloads, and structured failures. Extension packaging must keep the Go and PHP companions version-compatible without requiring Spinner's shared libraries to import a PHP runtime.
