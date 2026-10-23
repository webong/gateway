<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contract\ProtocolPlanner;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayDelivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

final class WebSocketSmokeProtocolPlanner implements ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        if ($event->protocol !== Protocol::WEBSOCKET) {
            return new GatewayDecision(
                protocol: Protocol::WEBSOCKET,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'WebSocket smoke route is not configured.',
            );
        }

        if ($event->kind === EventKind::CONNECT || $event->kind === EventKind::CLOSE) {
            return new GatewayDecision(
                protocol: Protocol::WEBSOCKET,
                action: GatewayAction::ACCEPT,
            );
        }

        $receiver = getenv('GATEWAY_WS_RECEIVER_URL');
        if ($event->kind !== EventKind::MESSAGE || ! is_string($receiver) || $receiver === '') {
            return new GatewayDecision(
                protocol: Protocol::WEBSOCKET,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'WebSocket smoke receiver is not configured.',
            );
        }

        return new GatewayDecision(
            protocol: Protocol::WEBSOCKET,
            action: GatewayAction::DELIVER,
            deliveries: [new GatewayDelivery(
                protocol: Protocol::HTTP,
                target: $receiver,
                subscriberId: 'websocket-smoke-subscriber',
                payload: $event->payload,
            )],
        );
    }
}
