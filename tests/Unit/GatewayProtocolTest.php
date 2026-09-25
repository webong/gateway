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
            adapter: 'smtp',
            subscriberId: 'subscriber-1',
            attributes: ['mail_from' => 'sender@example.test'],
            payload: 'message',
        )],
    );

    $serialized = $decision->toArray();

    expect($serialized['protocol'])->toBe('smtp')
        ->and($serialized['action'])->toBe('deliver')
        ->and($serialized['deliveries'][0]['adapter'])->toBe('smtp')
        ->and($serialized['deliveries'][0]['target'])->toBe('smtp:subscriber.example.test')
        ->and(base64_decode($serialized['deliveries'][0]['payload'], true))->toBe('message');
});

it('round trips DNS query events and serializes a synchronous reply', function (): void {
    $event = new GatewayEvent(
        id: 'dns-event-1',
        protocol: Protocol::DNS,
        kind: EventKind::QUERY,
        route: '/hook-1',
        host: 'payload.hook-1.dns.example.test.',
        attributes: ['qtype' => 'TXT', 'data' => 'payload'],
        payload: '{"data":"payload"}',
    );

    $roundTripped = GatewayEvent::fromArray($event->toArray());
    $decision = new GatewayDecision(
        protocol: Protocol::DNS,
        action: GatewayAction::DELIVER,
        reply: new GatewayDelivery(
            protocol: Protocol::HTTP,
            target: 'https://reply.example.test/dns',
        ),
    );

    expect($roundTripped->protocol)->toBe(Protocol::DNS)
        ->and($roundTripped->kind)->toBe(EventKind::QUERY)
        ->and($roundTripped->attributes['qtype'])->toBe('TXT')
        ->and($decision->toArray()['reply']['target'])->toBe('https://reply.example.test/dns');
});

it('rejects unsafe delivery adapter names', function (): void {
    expect(fn (): GatewayDelivery => new GatewayDelivery(
        protocol: Protocol::SMTP,
        target: 'account-42',
        adapter: '../../plugin',
    ))->toThrow(InvalidArgumentException::class, 'Gateway delivery adapter is invalid.');
});
