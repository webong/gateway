<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use Closure;
use LogicException;
use Webong\Gateway\Contracts\RoutePlanner;

/** Selects the installed extension that owns the default application route planner. */
final class RoutePlannerRegistry
{
    /** @var Closure(): RoutePlanner|null */
    private ?Closure $default = null;

    /** @param Closure(): RoutePlanner $factory */
    public function registerDefault(Closure $factory): void
    {
        if ($this->default !== null) { throw new LogicException('A default route planner is already registered.'); }
        $this->default = $factory;
    }

    public function resolveDefault(): ?RoutePlanner
    {
        return $this->default === null ? null : ($this->default)();
    }
}
