# Static and dynamic PHP companion lifecycle

Gateway extensions can contribute behavior to both product planes without
making Spinner a PHP host:

- Spinner runs the extension's Go guest and owns ingress, egress, retries, and
  delivery policy.
- Planner runs the extension's trusted PHP companion and owns activation,
  application integration, execution records, and capability advertisement.

The same action contract works whether Planner and Spinner share one Router
process or run as separate services. The deployment changes the transport, not
the extension contract.

## What this implementation added

Planner now treats a companion registration as a **candidate**, not as an
automatic override. A candidate has an extension identity, action name,
version, mode, and provider. The two supported modes are:

- `static`: a Composer-installed Laravel handler resolved from Planner's
  container;
- `dynamic`: a reviewed `ext.package/v1` release launched as a self-contained
  `php-jsonline` process.

The generic `extension` configuration resource is the source of truth for
which candidate is active. This gives Planner CMS ownership of the runtime
choice without introducing a companion-specific database table.

Both candidates may be installed at the same time. Only the selected candidate
is advertised or invoked. Re-registering the same provider is idempotent;
conflicting providers are rejected. If the selected candidate is missing,
disabled, has the wrong version, or fails its digest check, execution fails
closed instead of falling back to the other mode.

## End-to-end flow

1. An operator installs a static Composer package, a dynamic ext package, or
   both.
2. Planner discovers the available candidates. Discovery does not activate
   them.
3. The operator creates or updates an `/extensions` resource choosing
   `static` or `dynamic`, with optional version and digest pins.
4. Planner advertises only the active action capability, including its
   extension, mode, and activation generation.
5. A Spinner guest requests one exact action through `companion.Runner`.
6. A bundled Router uses the in-process adapter. A separately deployed Spinner
   uses the existing private, authenticated Planner connection.
7. Planner records the idempotent execution and invokes the active provider.
8. The companion returns a structured result. Spinner validates it and resumes
   the Go-owned automation or delivery flow.

An action invocation contains data, identity, version, deadline, execution ID,
and idempotency key. It never contains PHP source.

## Static mode

Static mode is for companions that deliberately need services from the
provided Laravel application. The extension's service provider registers its
handler with `CompanionActionRegistry`, and Planner resolves that handler from
Laravel's container.

Static code follows the application's Composer and deployment lifecycle. A
long-running Planner host must be reloaded after the package changes. Existing
static integrations remain active when no extension activation resource exists
so upgrading Gateway does not silently disable them.

Choose static mode when the action needs direct access to application-owned
repositories, service objects, or transaction boundaries.

## Dynamic mode

Dynamic mode is for a portable extension release whose PHP half should arrive
with its Go half instead of modifying Planner's root Composer installation.
The package declares:

- a named `spinner` entrypoint for the Go guest;
- a named `planner` entrypoint using `php-jsonline`;
- the Spinner descriptor at the package's top level;
- the exact Planner descriptor in
  `metadata["gateway.companion/v1"].descriptor`;
- the same package identity, version, and revision for both planes.

Planner reads dynamic packages from `GATEWAY_COMPANION_PLUGIN_DIR`, or from
`GATEWAY_PLUGIN_DIR` when both planes intentionally share one ext store. It
uses a PHP CLI executable; PHP-FPM deployments must identify that executable
with `GATEWAY_COMPANION_PHP_BINARY`.

Dynamic packages are self-contained. Their Composer `vendor/` tree is part of
the reviewed artifact, and their protocol executable serves the action through
newline-delimited JSON. Planner supports two execution policies for a selected
dynamic provider:

- `isolated` starts a fresh process for every invocation and remains the
  default;
- `warmed` reuses one process for one installed package revision within a
  Planner worker.

`warmed` is deliberately not a multi-process pool. Planner retires the process
when its activation generation changes, its invocation count, age, or idle
limit is reached, or it crashes, times out, changes its protocol output, or
fails package verification. A replacement must complete the reviewed
descriptor handshake before it receives an invocation.

Discovery verifies the installed package and copies it into a private,
digest-addressed generation under Planner's cache storage. Planner makes that
generation read-only and invokes only from it. It fully verifies the prepared
generation before an isolated launch and before starting or replacing a
warmed process; repeated calls already served by the same warmed process do
not rehash the complete package tree. Changing the installed source therefore
cannot change an active provider, and the changed source must pass normal
package verification during the next Planner discovery before it can become a
new generation.

Choose dynamic mode when the companion is portable, does not need Laravel's
container, and should be installed or rolled back with the extension bundle.

## Why duplicate registration does not happen

Static discovery and dynamic discovery populate separate candidate slots. The
activation resource then chooses one slot for the extension. Planner never
lets discovery order decide which implementation wins, and it never advertises
both as active.

The activation generation is included in the advertised capability. Operators
and Spinner can therefore distinguish a real activation change from repeated
discovery of the same installed code.

Deleting an activation returns a statically installed companion to the
backward-compatible static default. It does not make a dynamic package active
implicitly.

## Package integrity and trust

Companions are trusted operator-installed code, not tenant scripts and not a
PHP sandbox. The dynamic boundary still narrows what is passed into the code:

- Planner verifies the package manifest, declared artifacts, and hashes;
- symbolic links and unlisted package files are rejected;
- verified contents are copied to a private, digest-addressed, read-only
  generation;
- that generation's digest is checked again before each process launch, but
  not before every request already handled by one warmed process;
- the process receives a scrubbed environment rather than Planner's complete
  application environment;
- the ext handshake, protocol version, descriptor, and action name must match;
- time, input, and output limits bound the protocol exchange.

Process separation does not make hostile PHP safe. Only reviewed packages
should be installed in the ext store.

## HookScript example

HookScript is the first extension using both modes. Its Go guest declares only
`gateway.hookscript.execute`. The PHP companion evaluates the Primi-compatible
script and returns a language-neutral automation outcome.

In static mode, `webong/gateway-hookscript` registers the handler through
Laravel package discovery. In dynamic mode, the HookScript package builder
creates a combined ext package containing the Go guest and the self-contained
PHP companion. Switching the `/extensions` resource changes which PHP provider
runs; Spinner continues to invoke the same action contract.

Build a local dynamic package with:

```bash
cd pkg/hookscript
composer install --working-dir=companion --no-dev --classmap-authoritative
mkdir -p /tmp/gateway-extensions
php scripts/build-ext-package /tmp/gateway-extensions/webong%2fhookscript 1.0.0
```

After installing that directory in the configured ext store, enable it through
Planner:

```http
POST /extensions
Content-Type: application/json

{
  "extension": "webong/hookscript",
  "enabled": true,
  "mode": "dynamic",
  "version": "1.0.0",
  "execution": {
    "mode": "warmed",
    "max_invocations": 100,
    "max_age_seconds": 300,
    "idle_timeout_seconds": 30
  }
}
```

Omit `execution` to use `isolated`. The limits are desired state owned by
Planner, not deployment environment variables. `warmed` is rejected for a
static provider. It is most useful with a long-running Planner such as Octane;
a short-lived PHP-FPM request cannot preserve a child beyond the lifetime of
the PHP process that owns it.

Use `GET /extensions` to inspect the desired mode, discovered candidate,
readiness, active capabilities, and activation generation before routing work
to it.

## Current boundary

The registry, activation API, package verification, isolated and bounded
warmed execution, dynamic PHP guest, Spinner reverse call, combined-package
entrypoint selection, HookScript integration, and focused regression coverage
are implemented. Multi-worker process pooling remains deliberately deferred
until production traffic demonstrates that one warmed worker per package
revision is insufficient.

See [PHP companion actions](companion-actions.md) for the programming contract
and [ADR 0004](adr/0004-invoke-trusted-php-companion-actions-through-planner.md)
for the architectural decision.
