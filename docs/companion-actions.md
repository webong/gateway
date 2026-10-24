# PHP companion actions

A Gateway extension can ship Go behavior for Spinner and trusted PHP behavior
for the Planner host. PHP companions are installed application code. Spinner
invokes them by action name with typed JSON data; neither the public Planner API
nor the private invocation interface accepts PHP source.

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
    );
}
```

The handler is resolved through Laravel's container, so its constructor may
request only the application services that the companion intentionally uses.
Changing the installed companion set requires the normal package deployment
and a reload of long-running PHP workers.

## Invoke from Spinner

Spinner extensions depend on the `companion.Runner` interface from the driver
module. Bundled Router hosts provide an in-process adapter; standalone Spinner
hosts provide a private HTTP adapter using the configured Planner connection.

```go
result, err := runner.Invoke(ctx, companion.Invocation{
    ProtocolVersion:  companion.ProtocolVersion,
    Action:           "email.create-ticket",
    CompanionVersion: "1.0.0",
    ExecutionID:      executionID,
    IdempotencyKey:   messageID,
    Input: map[string]any{
        "sender":  sender,
        "subject": subject,
    },
})
```

Use capability discovery before dispatch when a plugin can operate without its
companion. Missing or version-mismatched actions fail closed.

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

The private endpoints are:

- `GET /_internal/gateway/companion-actions`
- `POST /_internal/gateway/companion-actions/{action}/executions`

Both require `X-Gateway-Internal` and are hidden by Spinner's public edge.
