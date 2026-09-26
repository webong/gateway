<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayEvent;

/**
 * Protocol-neutral planner boundary for session-oriented adapters such as
 * WebSocket and SMTP. HTTP remains on RoutePlanner for compatibility.
 */
interface ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision;
}
