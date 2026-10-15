<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;

/** Operator-owned, exact authorization for registry-managed local hooks. */
final class ShellSubscriptionPolicy
{
    public function assertAllowed(string $endpointKey, string $subscriberId, string $hook): void
    {
        $allowed = config('gateway.shell.subscription_hooks', []);
        if (! is_array($allowed)) {
            throw new InvalidArgumentException('Shell subscription hook policy must be a map.');
        }

        $subscribers = $allowed[$endpointKey] ?? null;
        $hooks = is_array($subscribers) ? ($subscribers[$subscriberId] ?? null) : null;
        if (! is_array($hooks) || ! array_is_list($hooks) || ! in_array($hook, $hooks, true)) {
            throw new InvalidArgumentException('The Shell hook is not authorized for this endpoint and subscriber.');
        }
    }
}
