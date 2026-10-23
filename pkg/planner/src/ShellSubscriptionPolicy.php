<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;

/** Operator-owned, exact authorization for registry-managed local or SSH hooks. */
final readonly class ShellSubscriptionPolicy
{
    public function __construct(private ShellHookGrantStore $grants) {}

    /** @param array<string, mixed>|null $ssh */
    public function assertAllowed(string $endpointKey, string $subscriberId, string $hook, ?array $ssh = null): void
    {
        if (! $this->grants->allowed($endpointKey, $subscriberId, $hook, $ssh)) {
            throw new InvalidArgumentException('The Shell hook is not authorized for this endpoint and subscriber.');
        }
    }
}
