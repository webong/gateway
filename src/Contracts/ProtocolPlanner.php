<?php

declare(strict_types=1);

namespace Webong\NetGateway\Contracts;

use Webong\NetGateway\Protocol\GatewayDecision;
use Webong\NetGateway\Protocol\GatewayEvent;

/**
 * Future protocol-neutral planner boundary for WebSocket and SMTP adapters.
 * HTTP remains on RoutePlanner until its v1 contract is intentionally retired.
 */
interface ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision;
}
