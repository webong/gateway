<?php

declare(strict_types=1);

namespace Webong\Gateway\Deliveries;

use InvalidArgumentException;

/** Retry-safe mutations registered by the extension that owns the operation. */
final class DeliveryAdminReplayPolicyRegistry
{
    /** @var array<string, true> */
    private array $operations = [];

    public function register(string $method, string $path): void
    {
        if (preg_match('/\\A[A-Z]{3,10}\\z/D', $method) !== 1
            || preg_match('/\\A\\/(?:[a-z0-9._-]+\\/?)*\\z/D', $path) !== 1) {
            throw new InvalidArgumentException('Invalid delivery admin replay operation.');
        }

        $key = $method.' '.$path;
        if (isset($this->operations[$key])) {
            throw new InvalidArgumentException("Delivery admin replay operation [{$key}] is already registered.");
        }

        $this->operations[$key] = true;
    }

    public function allows(string $method, string $path): bool
    {
        return isset($this->operations[$method.' '.$path]);
    }
}
