---
status: accepted
---

# Invoke static or dynamic PHP companion actions through Planner

A Gateway extension may pair its Spinner implementation with a trusted PHP Companion Action. Planner supports two adapters behind the same registry seam: a static action resolved from Laravel's container, and a self-contained PHP process installed as an `ext.package/v1` artifact. Both may be discovered at once, but Planner's generic extension resource explicitly enables exactly one mode. Spinner invokes the active provider by stable action name with typed input and receives a structured result. Spinner never uploads raw PHP through the invocation interface.

## Contract

- The companion package owns its PHP source, dependencies, autoload rules, action names, and input/output schemas.
- Static packages offer Laravel handler classes. Dynamic packages declare a `planner` entrypoint using the `php-jsonline` runtime and `ext.plugin/v1`; Planner verifies the installed package before launching it.
- Registration discovers candidates. It does not activate them. The generic `extension` configuration selects `static` or `dynamic`; dynamic desired state pins an HTTPS artifact URL, release version, archive SHA-256, optional extracted-package digest, and activation generation.
- Repeated discovery of the same provider identity is idempotent. Two different providers for the same action and mode, or two extensions claiming the same action name, are conflicts rather than order-dependent overrides.
- Planner's private Action Runner resolves only the enabled provider and authenticates Spinner using the existing internal trust seam. The Spinner-to-Planner adapter may be in-process or private HTTP, but it carries the same versioned invocation contract.
- An invocation contains an action name, companion version or digest, execution ID, deadline, idempotency key, and size-bounded typed input. Its result is structured data or a declared failure; PHP output is never treated as an implicit protocol.
- Static installation, upgrade, rollback, and removal follow the application deployment lifecycle. For dynamic installation, Planner and Spinner independently pull and verify one combined release into plane-local ext stores and select only their named entrypoint. It does not modify Planner's root Composer autoloader or require PHP in Spinner. Planner materializes a private, digest-addressed, read-only generation and executes only that pinned generation.
- Planner advertises only active action capabilities. Capabilities include extension identity, selected mode and activation generation so Spinner and operators can observe what is enabled.
- Portable plugins declare every action they may invoke through the extension protocol's `gateway.companion/v1` reverse contract. Spinner supplies the runner as a host capability; plugins never receive Planner credentials.
- Dynamic providers default to an isolated process per invocation. Planner may select bounded `warmed` execution, which reuses one process per package revision and Planner worker and retires it on policy generation, invocation-count, age, idle, integrity, deadline, crash, or protocol boundaries. Deployment runtimes may provide a bounded pool of persistent Planner workers; each worker retains an independent Laravel container and companion child, and the companion contract exposes no scheduler or pool internals.

## Trust boundary

PHP Companion Actions are trusted operator-installed code. Static handlers run with the privileges of the Planner application, including services deliberately injected into them. Dynamic handlers run as child processes and receive only their packaged code, typed invocation and deliberately granted process environment; process separation alone is not an OS sandbox. Neither mode is a sandbox for tenant-authored or request-supplied PHP.

Planner's public CMS API may select an installed extension and configure a route to an active action, but it cannot create an action by accepting PHP source. The private Action Runner accepts invocation data only. Dynamic processes cannot reach Laravel's container directly; application integration must use separately declared Planner host contracts.

## Considered options

- Send raw PHP source from Spinner for every execution. Rejected because it turns the Planner boundary into a remote-code-execution API, repeats code transfer, complicates provenance and caching, and risks state leakage in long-running PHP workers.
- Require every companion to be statically installed into Planner's root Composer application. Rejected as the only lifecycle because it prevents one ext bundle from dynamically contributing behavior to both Gateway planes. Retained as a supported mode for tightly integrated application code.
- Let a dynamically discovered provider silently override or fall back to a static provider. Rejected because load order and process health would change behavior without a Planner revision. Mode selection is explicit and failures remain closed.
- Execute companion PHP directly inside Spinner through an embedded FrankenPHP engine. Rejected as the companion-action contract because it couples extension behavior to one PHP host and does not run inside the provided application's container and service graph. Raw PHP Automation is not part of the default distribution; if it becomes a supported product surface, it must ship as an explicitly installed runtime with its own package, tests, and security contract.
- Invoke an application-owned HTTP endpoint without a Gateway action contract. Supported as an ordinary delivery, but insufficient for installed companions because capability discovery, version compatibility, idempotency, deadlines, and structured failures would be application-specific.

## Consequences

Gateway provides a transport-neutral Action Runner contract, a multi-provider Planner registry, generic activation state, verified ext package discovery, a PHP JSON-line guest, Spinner adapters, and a least-privilege reverse-call adapter for portable plugins. Execution records use expiring leases and encrypted results; retries after an uncertain crash are at-least-once and companion handlers remain responsible for idempotent external effects. Extension packaging keeps Go and PHP processes version-compatible without requiring Spinner's shared libraries to import a PHP runtime.

## Implementation status

- [x] Define a framework-free companion provider interface and versioned capability metadata.
- [x] Adapt Composer-installed Laravel handlers as `static` candidates.
- [x] Store provider activation in Planner's generic `extension` configuration resource.
- [x] Expose authenticated `/extensions` management and readiness endpoints.
- [x] Discover and verify dynamic companion entrypoints from `ext.package/v1` releases.
- [x] Run `php-jsonline` companions with deadlines, bounded protocol messages, a scrubbed environment, and a verified content-addressed generation checked before every launch.
- [x] Route bundled and separately hosted Spinner calls through the same companion contract.
- [x] Support named `spinner` and `planner` entrypoints in one versioned extension package.
- [x] Store artifact URL, version, and archive digest as Planner-owned desired state and let each separately hosted plane independently pull, verify, and reconcile its entrypoint.
- [x] Keep PHP and HookScript out of the default Spinner while supporting HookScript installation after Spinner startup.
- [x] Prevent double registration through separate candidate slots, explicit activation, idempotent discovery, and conflict rejection.
- [x] Convert HookScript into the first extension with interchangeable static and dynamic companions.
- [x] Cover activation switching, absence, incompatibility, duplicate discovery, real subprocess execution, and package tampering with focused regression tests.
- [x] Support Planner-owned isolated and bounded warmed execution without adding a pool to the companion provider.
- [x] Run a release-image smoke with the complete FrankenPHP build toolchain and published package layout.
- [x] Support opt-in runtime-level pooling through `GATEWAY_PLANNER_WORKERS`, with one independently bounded warmed child per package revision and Planner worker.

The implemented lifecycle is described in
[Static and dynamic PHP companion lifecycle](../php-companion-lifecycle.md).
