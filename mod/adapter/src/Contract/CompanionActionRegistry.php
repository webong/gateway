<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

interface CompanionActionRegistry
{
    /** @param class-string<CompanionAction> $handler */
    public function register(
        string $name,
        string $handler,
        ?string $version = null,
        ?string $digest = null,
        ?string $extension = null,
    ): void;

    public function registerProvider(CompanionActionProvider $provider): void;

    /** @return list<CompanionActionCapability> */
    public function capabilities(): array;

    public function capability(string $name): ?CompanionActionCapability;

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult;

    /** @return list<array<string, mixed>> */
    public function statuses(): array;
}
