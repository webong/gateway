<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use Closure;
use LogicException;
use Webong\Gateway\Contracts\RoutePlanner;

/** Selects an installed route-kind planner after the core resolves an ingress path. */
final class RoutePlannerRegistry
{
    /** @var array<string, Closure(): RoutePlanner> */
    private array $factories = [];

    /** @param Closure(): RoutePlanner $factory */
    public function register(string $kind, Closure $factory): void
    {
        if (preg_match('/\A[a-z][a-z0-9.-]{0,63}\z/D', $kind) !== 1) {
            throw new LogicException("Invalid route kind [{$kind}].");
        }
        if (isset($this->factories[$kind])) {
            throw new LogicException("Route kind [{$kind}] is already registered.");
        }
        $this->factories[$kind] = $factory;
    }

    public function resolve(string $kind): ?RoutePlanner
    {
        return isset($this->factories[$kind]) ? ($this->factories[$kind])() : null;
    }
}
