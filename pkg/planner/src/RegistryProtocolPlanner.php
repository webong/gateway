<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayEvent;

/** Dispatches protocol policy to the installed Planner Extension that owns it. */
final readonly class RegistryProtocolPlanner implements ProtocolPlanner
{
    public function __construct(
        private ProtocolPlannerRegistry $planners,
        private RouteProtocolPlanner $fallback,
    ) {}

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        return ($this->planners->resolve($event->protocol) ?? $this->fallback)->planEvent($event);
    }
}
