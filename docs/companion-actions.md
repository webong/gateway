# PHP companion actions

A Gateway extension can ship Go behavior for Spinner and trusted PHP behavior
for the Planner host. A PHP companion may be a static Laravel handler or a
self-contained process installed from the same ext bundle as the Go guest.
Spinner invokes it by action name with typed JSON data; neither the public
Planner API nor the private invocation interface accepts PHP source.

For the complete installation, activation, and cross-plane lifecycle, see
[Static and dynamic PHP companion lifecycle](php-companion-lifecycle.md).

## Register a Planner companion

The companion package owns its handler and registers it from its Laravel
provider:

```php
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final class CreateTicket implements CompanionAction
{
    public function __construct(private TicketRepository $tickets) {}

    public function handle(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $ticket = $this->tickets->create($invocation->input);

        return CompanionActionResult::succeeded($invocation->executionId, [
            'ticket_id' => $ticket->id,
        ]);
    }
}

public function boot(CompanionActionRegistry $actions): void
{
    $actions->register(
        'email.create-ticket',
        CreateTicket::class,
        version: '1.0.0',
        extension: 'example/email-ticket',
    );
}
```

The handler is resolved through Laravel's container, so its constructor may
request only the application services that the companion intentionally uses.
This offers a `static` candidate. Changing static code requires the normal
application deployment and a reload of long-running PHP workers.

## Ship a dynamic Planner companion

An ext package can also declare a self-contained PHP JSON-line process:

```json
{
  "apiVersion": "ext.package/v1",
  "descriptor": {
    "apiVersion": "ext.plugin/v1",
    "identity": {
      "id": "example/email-ticket",
      "revision": "sha256-or-release-revision",
      "version": "1.0.0"
    },
    "contracts": [{
      "name": "gateway.companion",
      "version": "v1",
      "operations": [{"name": "email.create-ticket"}]
    }]
  },
  "artifacts": [{
    "name": "planner-companion",
    "path": "bin/companion",
    "sha256": "..."
  }],
  "entrypoints": [{
    "name": "planner",
    "runtime": "php-jsonline",
    "artifact": "planner-companion",
    "protocols": ["ext.plugin/v1"]
  }]
}
```

Set `GATEWAY_COMPANION_PLUGIN_DIR` to the deployment-owned ext store location.
Planner verifies the manifest and every declared file/tree artifact, rejects
unlisted files and symbolic links, completes the exact ext handshake, and
starts the PHP process with protocol output isolated from PHP diagnostics.
Dynamic mode requires `proc_open` and a PHP CLI executable. CLI and Octane
deployments normally use the `PHP_BINARY` default. PHP-FPM deployments must set
`GATEWAY_COMPANION_PHP_BINARY` to their CLI executable, for example
`/usr/local/bin/php`; the FPM executable itself is not a script runner. Reload
long-running Planner workers after installing, upgrading, or removing package
files so discovery sees one stable reviewed release.

The package's `planner` entrypoint uses `CompanionProcessGuest` to serve its
installed actions. A combined release can also declare a `spinner` entrypoint.
The top-level descriptor remains the Spinner process descriptor; the exact
Planner process descriptor goes in
`metadata["gateway.companion/v1"].descriptor` with the same identity. Each
plane selects its named entrypoint and verifies the descriptor for the process
it launches. The package's own Composer `vendor/` tree is an artifact inside
the package; Planner's root Composer installation is never changed.

## Enable exactly one provider

Discovery does not decide which candidate runs. Planner stores activation in
the generic `configurations` CMS as kind `extension`. Manage it through:

- `GET /extensions`
- `POST /extensions`
- `GET /extensions/{activation-id}`
- `PUT /extensions/{activation-id}`
- `DELETE /extensions/{activation-id}`

For example:

```json
{
  "extension": "example/email-ticket",
  "enabled": true,
  "mode": "dynamic",
  "version": "1.0.0",
  "digest": "optional-package-sha256",
  "execution": {
    "mode": "warmed",
    "max_invocations": 100,
    "max_age_seconds": 300,
    "idle_timeout_seconds": 30
  }
}
```

An extension without an activation resource retains its static candidate for
backward compatibility. Selecting `dynamic` never silently falls back to
`static` if the process is absent or incompatible. Repeated discovery of the
same candidate is idempotent; different candidates for one mode are rejected.

Dynamic companions default to `execution.mode: isolated`, which launches a
fresh child for every invocation. `warmed` keeps one child per package revision
inside a long-running Planner worker and bounds reuse by invocation count,
absolute age, and idle time. Any policy revision, package-integrity failure,
deadline, crash, or protocol failure discards the child. This avoids paying PHP
startup cost for every call without introducing a multi-worker pool or moving
lifecycle policy into deployment environment variables.

Planner never executes a dynamic companion from the mutable installation
directory. Discovery verifies the package and materializes a private,
digest-addressed, read-only generation. Each isolated launch and each new
warmed process fully verifies that generation. Calls handled by an existing
warmed process skip the full-tree hash, so integrity work is paid at a process
lifecycle boundary rather than on every invocation.

## Invoke from Spinner

Spinner extensions depend on the `companion.Runner` interface from the driver
module. Bundled Router hosts provide an in-process adapter; standalone Spinner
hosts provide a private HTTP adapter using the configured Planner connection.

```go
deadline := time.Now().Add(30 * time.Second)
result, err := runner.Invoke(ctx, companion.Invocation{
    ProtocolVersion:  companion.ProtocolVersion,
    Action:           "email.create-ticket",
    CompanionVersion: "1.0.0",
    ExecutionID:      executionID,
    IdempotencyKey:   messageID,
    Deadline:         &deadline,
    Input: map[string]any{
        "sender":  sender,
        "subject": subject,
    },
})
```

Use capability discovery before dispatch when a plugin can operate without its
companion. Missing or version-mismatched actions fail closed.

Portable process and native Go automation plugins use the existing ext
reverse-call interface instead of receiving a Planner URL or internal token.
Their reviewed descriptor declares `gateway.companion/v1` with every exact
action name the plugin may call:

```go
Reverse: []plugin.Contract{{
    ContractRef: plugin.ContractRef{Name: "gateway.companion", Version: "v1"},
    Operations: []plugin.Operation{{Name: "email.create-ticket"}},
}},
```

Inside a plugin call, `plugin.ReverseFrom(ctx)` invokes that declared operation
with a JSON-encoded `companion.Invocation`. Spinner rejects action substitution:
the operation name and invocation action must match. A generic permission to
invoke every companion action does not exist.

## Package lifecycle

One extension release can contain two operator-reviewed entrypoints:

- a Go process whose descriptor declares the exact reverse actions it needs;
  and
- a self-contained Composer-powered PHP process providing those actions.

Planner CMS is the desired-state owner. Spinner and Planner each reconcile the
entrypoint intended for their plane from the same reviewed package identity.
The action's registered version and package digest are the rollout compatibility
check. Spinner never copies PHP source in an invocation. Static Composer
packages remain available when direct Laravel container access is required.

## Execution semantics

Planner records each execution before calling PHP. Reusing an idempotency key
with identical input returns the original terminal result without running the
handler again. Reusing it with different input returns a conflict. If another
caller observes a claimed execution without a terminal result, Planner returns
`in_progress`; Spinner must not retry it as though it definitely never ran.

Action failures are structured results. Transport failures, malformed results,
authentication failures, and capability mismatches remain invocation errors.
The caller's deadline bounds its wait, while handlers should also inspect the
invocation deadline before starting expensive or irreversible work.

Running executions are leased until their invocation deadline. A retry may
reclaim an expired lease after a crash; an ownership token prevents a late old
worker from overwriting the newer result. This is at-least-once execution after
an uncertain failure, so action handlers must use the supplied idempotency key
for irreversible downstream effects. Stored results are encrypted at rest and
`gateway:prune-companion-actions` removes records outside the configured
idempotency window.

The private endpoints are:

- `GET /_internal/gateway/companion-actions`
- `POST /_internal/gateway/companion-actions/{action}/executions`

Both require `X-Gateway-Internal` and are hidden by Spinner's public edge.

## First bundled use: HookScript

The HookScript automation extension is the first bundled consumer. Its Go guest
declares only `gateway.hookscript.execute`, sends HookScript source and event
data as action input, and receives a language-neutral automation outcome. The
matching PHP code may run statically through Laravel or dynamically through
`pkg/hookscript/bin/companion`; both expose the same versioned action. Build a
self-contained dynamic artifact with:

```bash
cd pkg/hookscript
composer install --working-dir=companion --no-dev --classmap-authoritative
mkdir -p /tmp/gateway-extensions
php scripts/build-ext-package /tmp/gateway-extensions/webong%2fhookscript 1.0.0
```

The dynamic build uses a dedicated, framework-free Composer lock under
`pkg/hookscript/companion`. It contains the ext protocol, HookScript runtime,
and interpreter without copying Laravel or the Planner host into each package.
Planner still verifies the complete installed tree before every invocation,
including invocations handled by a warmed process.

This keeps FrankenPHP and PHP hosting details out of the Go extension while
preserving Go-owned ingress, egress, scheduling, and outcome validation.
