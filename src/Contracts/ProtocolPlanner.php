<?php

declare(strict_types=1);

namespace Webong\WebRelay\Contracts;

use Webong\WebRelay\Protocol\GatewayDecision;
use Webong\WebRelay\Protocol\GatewayEvent;

/**
 * Future protocol-neutral planner boundary for WebSocket and SMTP adapters.
 * HTTP remains on RoutePlanner until its v1 contract is intentionally retired.
 */
interface ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision;
}
