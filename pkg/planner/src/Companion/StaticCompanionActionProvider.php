<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Contracts\Container\Container;
use InvalidArgumentException;
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final readonly class StaticCompanionActionProvider implements CompanionActionProvider
{
    /** @param class-string<CompanionAction> $handler */
    public function __construct(
        private Container $container,
        private string $extensionId,
        private string $handler,
        private CompanionActionCapability $providedCapability,
    ) {
        if (! is_a($handler, CompanionAction::class, true)) {
            throw new InvalidArgumentException("Gateway companion action handler [{$handler}] must implement CompanionAction.");
        }
    }

    public function extension(): string
    {
        return $this->extensionId;
    }

    public function mode(): string
    {
        return 'static';
    }

    public function providerId(): string
    {
        return 'static:'.$this->handler;
    }

    public function capability(): CompanionActionCapability
    {
        return $this->providedCapability;
    }

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $handler = $this->container->make($this->handler);
        if (! $handler instanceof CompanionAction) {
            throw new InvalidArgumentException("Gateway companion action handler [{$this->handler}] must implement CompanionAction.");
        }

        return $handler->handle($invocation);
    }
}
