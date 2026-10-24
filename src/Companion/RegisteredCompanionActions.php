<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Contracts\Container\Container;
use InvalidArgumentException;
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final class RegisteredCompanionActions implements CompanionActionRegistry
{
    /** @var array<string, array{capability: CompanionActionCapability, handler: class-string<CompanionAction>}> */
    private array $actions = [];

    public function __construct(private readonly Container $container) {}

    public function register(string $name, string $handler, ?string $version = null, ?string $digest = null): void
    {
        $capability = new CompanionActionCapability($name, $version, $digest);
        if (! is_a($handler, CompanionAction::class, true)) {
            throw new InvalidArgumentException("Gateway companion action handler [{$handler}] must implement CompanionAction.");
        }
        $existing = $this->actions[$name] ?? null;
        if ($existing !== null) {
            if ($existing['handler'] === $handler && $existing['capability']->toArray() === $capability->toArray()) {
                return;
            }
            throw new InvalidArgumentException("Gateway companion action [{$name}] is already registered.");
        }

        $this->actions[$name] = ['capability' => $capability, 'handler' => $handler];
        ksort($this->actions);
    }

    public function capabilities(): array
    {
        return array_values(array_map(
            static fn (array $definition): CompanionActionCapability => $definition['capability'],
            $this->actions,
        ));
    }

    public function capability(string $name): ?CompanionActionCapability
    {
        return $this->actions[$name]['capability'] ?? null;
    }

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $definition = $this->actions[$invocation->action] ?? null;
        if ($definition === null) {
            throw new CompanionActionNotFound("Gateway companion action [{$invocation->action}] is not installed.");
        }
        $capability = $definition['capability'];
        if ($invocation->companionVersion !== null && $invocation->companionVersion !== $capability->version) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] version is incompatible.");
        }
        if ($invocation->companionDigest !== null && $invocation->companionDigest !== $capability->digest) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] digest is incompatible.");
        }

        $handler = $this->container->make($definition['handler']);
        if (! $handler instanceof CompanionAction) {
            throw new InvalidArgumentException("Gateway companion action handler [{$definition['handler']}] must implement CompanionAction.");
        }
        $result = $handler->handle($invocation);
        if ($result->executionId !== $invocation->executionId) {
            throw new InvalidArgumentException("Gateway companion action [{$invocation->action}] returned the wrong execution ID.");
        }
        if ($result->status === CompanionActionResult::IN_PROGRESS) {
            throw new InvalidArgumentException("Gateway companion action [{$invocation->action}] cannot return an in-progress result.");
        }

        return $result;
    }
}
