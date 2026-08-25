<?php

declare(strict_types=1);

use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayDelivery;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

it('round trips a protocol-neutral gateway event', function (): void {
    $event = new GatewayEvent(
        id: 'event-1',
        protocol: Protocol::WEBSOCKET,
        kind: EventKind::MESSAGE,
        route: '/chat',
        sessionId: 'session-1',
        headers: ['X-Event' => ['message']],
        attributes: ['channel' => 'customer-42'],
        payload: 'hello',
    );

    $roundTripped = GatewayEvent::fromArray($event->toArray());

    expect($roundTripped->protocol)->toBe(Protocol::WEBSOCKET)
        ->and($roundTripped->kind)->toBe(EventKind::MESSAGE)
        ->and($roundTripped->sessionId)->toBe('session-1')
        ->and($roundTripped->payload)->toBe('hello')
        ->and($roundTripped->attributes['channel'])->toBe('customer-42');
});
it('serializes typed gateway delivery decisions', function (): void {
    $decision = new GatewayDecision(
        protocol: Protocol::SMTP,
        action: GatewayAction::DELIVER,
        deliveries: [new GatewayDelivery(
            protocol: Protocol::SMTP,
            target: 'smtp:subscriber.example.test',
            subscriberId: 'subscriber-1',
            attributes: ['mail_from' => 'sender@example.test'],
            payload: 'message',
        )],
    );

    $serialized = $decision->toArray();

    expect($serialized['protocol'])->toBe('smtp')
        ->and($serialized['action'])->toBe('deliver')
        ->and($serialized['deliveries'][0]['target'])->toBe('smtp:subscriber.example.test')
        ->and(base64_decode($serialized['deliveries'][0]['payload'], true))->toBe('message');
});
