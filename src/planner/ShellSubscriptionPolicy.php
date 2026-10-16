<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;

/** Operator-owned, exact authorization for registry-managed local hooks. */
final readonly class ShellSubscriptionPolicy
{
    public function __construct(private ShellHookGrantStore $grants) {}

    public function assertAllowed(string $endpointKey, string $subscriberId, string $hook): void
    {
        if (! $this->grants->allowed($endpointKey, $subscriberId, $hook)) {
            throw new InvalidArgumentException('The Shell hook is not authorized for this endpoint and subscriber.');
        }
    }
}
