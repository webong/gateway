<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use Closure;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

/** Publishes planned route outcomes to installed Planner Extensions. */
final class RoutePlanObserverRegistry
{
    /** @var array<string, Closure(IngressRequest, RoutePlan): void> */
    private array $observers = [];

    /** @param Closure(IngressRequest, RoutePlan): void $observer */
    public function register(string $module, Closure $observer): void
    {
        $this->observers[$module] = $observer;
    }

    public function observe(IngressRequest $request, RoutePlan $plan): void
    {
        foreach ($this->observers as $observer) {
            $observer($request, $plan);
        }
    }
}
