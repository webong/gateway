<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayEvent;

/**
 * Future protocol-neutral planner boundary for WebSocket and SMTP adapters.
 * HTTP remains on RoutePlanner until its v1 contract is intentionally retired.
 */
interface ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision;
}
