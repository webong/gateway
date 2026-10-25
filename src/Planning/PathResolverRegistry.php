<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use Closure;
use Webong\Gateway\Contracts\PathResolver;

/** Builds one resolver chain from independently installed Planner Extensions. */
final class PathResolverRegistry
{
    /** @var array<string, Closure(?PathResolver): PathResolver> */
    private array $factories = [];

    /** @param Closure(?PathResolver): PathResolver $factory */
    public function register(string $module, Closure $factory): void
    {
        $this->factories[$module] = $factory;
    }

    public function decorate(?PathResolver $fallback): ?PathResolver
    {
        foreach ($this->factories as $factory) {
            $fallback = $factory($fallback);
        }

        return $fallback;
    }
}
