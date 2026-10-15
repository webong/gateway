<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayDelivery;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

final class WebstreamSmokeProtocolPlanner implements ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        $source = getenv('GATEWAY_WEBSTREAM_SOURCE_URL');

        if ($event->protocol !== Protocol::WEBSTREAM || $event->kind !== EventKind::CONNECT || ! is_string($source) || $source === '') {
            return new GatewayDecision(protocol: Protocol::WEBSTREAM, action: GatewayAction::REJECT, statusCode: 404, message: 'Webstream smoke source is not configured.');
        }

        return new GatewayDecision(
            protocol: Protocol::WEBSTREAM,
            action: GatewayAction::STREAM,
            stream: new GatewayDelivery(
                protocol: Protocol::HTTP,
                target: $source,
                headers: ['Content-Type' => ['application/json'], ...(isset($event->attributes['last_event_id']) ? ['Last-Event-ID' => [$event->attributes['last_event_id']]] : [])],
                attributes: ['method' => $event->method, 'upload_mode' => $event->attributes['upload_mode'] ?? 'buffered'],
                payload: $event->payload,
            ),
        );
    }
}
