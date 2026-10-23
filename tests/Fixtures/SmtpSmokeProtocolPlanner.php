<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contract\ProtocolPlanner;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayDelivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

final class SmtpSmokeProtocolPlanner implements ProtocolPlanner
{
    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        $receiver = getenv('GATEWAY_SMTP_RECEIVER_URL');

        if ($event->protocol !== Protocol::SMTP || ! is_string($receiver) || $receiver === '') {
            return new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'SMTP smoke route is not configured.',
            );
        }

        return new GatewayDecision(
            protocol: Protocol::SMTP,
            action: GatewayAction::DELIVER,
            statusCode: 250,
            deliveries: [new GatewayDelivery(
                protocol: Protocol::HTTP,
                target: $receiver,
                subscriberId: 'smtp-smoke-subscriber',
            )],
        );
    }
}
