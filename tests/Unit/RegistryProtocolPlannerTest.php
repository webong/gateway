<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\HttpTransport;
use Webong\Gateway\Protocol\Protocol;
use Webong\Gateway\Protocol\RoutePlan;
use Webong\Gateway\RegistryProtocolPlanner;

it('adapts a registry route plan to a protocol-neutral SMTP delivery decision', function (): void {
    $adapter = new class implements RoutePlanner {
        public ?IngressRequest $received = null;

        public function plan(IngressRequest $request): RoutePlan
        {
            $this->received = $request;

            return RoutePlan::relay(
                relays: [new Delivery(
                    url: 'https://subscriber.example.test/mail',
                    subscriberId: 'subscriber-1',
                )],
            );
        }
    };
    $planner = new RegistryProtocolPlanner($adapter);

    $decision = $planner->planEvent(new GatewayEvent(
        id: 'smtp-event-1',
        protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION,
        route: 'recipient@example.test',
        host: 'gateway.example.test',
        headers: ['Last-Event-ID' => ['event-42']],
        attributes: ['mail_from' => 'sender@example.test'],
        payload: "Subject: hello\r\n\r\nmessage",
    ));

    expect($adapter->received)->toBeInstanceOf(IngressRequest::class)
        ->and($adapter->received->protocol)->toBe('smtp')
        ->and($adapter->received->event)->toBe('transaction')
        ->and($adapter->received->attributes['mail_from'])->toBe('sender@example.test')
        ->and($adapter->received->body)->toBe("Subject: hello\r\n\r\nmessage")
        ->and($decision->protocol)->toBe(Protocol::SMTP)
        ->and($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries[0]->protocol)->toBe(Protocol::HTTP)
        ->and($decision->deliveries[0]->subscriberId)->toBe('subscriber-1')
        ->and($decision->deliveries[0]->payload)->toBe("Subject: hello\r\n\r\nmessage");
});

it('preserves a DNS reply separately from asynchronous relays', function (): void {
    $adapter = new class implements RoutePlanner {
        public ?IngressRequest $received = null;

        public function plan(IngressRequest $request): RoutePlan
        {
            $this->received = $request;

            return RoutePlan::relay(
                reply: new Delivery(
                    url: 'https://reply.example.test/dns',
                    subscriberId: 'reply-subscriber',
                ),
                relays: [new Delivery(
                    url: 'https://relay.example.test/dns',
                    subscriberId: 'relay-subscriber',
                )],
            );
        }
    };
    $planner = new RegistryProtocolPlanner($adapter);

    $decision = $planner->planEvent(new GatewayEvent(
        id: 'dns-event-1',
        protocol: Protocol::DNS,
        kind: EventKind::QUERY,
        route: '/hook-1',
        host: 'payload.hook-1.dns.example.test.',
        attributes: ['qtype' => 'A', 'data' => 'payload'],
        payload: '{"data":"payload"}',
    ));

    expect($adapter->received?->scheme)->toBe('dns')
        ->and($adapter->received?->attributes['qtype'])->toBe('A')
        ->and($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->reply?->target)->toBe('https://reply.example.test/dns')
        ->and($decision->reply?->headers['Content-Type'])->toBe(['application/json'])
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->target)->toBe('https://relay.example.test/dns');
});

it('turns an unmatched protocol route into an SMTP rejection', function (): void {
    $planner = new RegistryProtocolPlanner(new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::passThrough();
        }
    });

    $decision = $planner->planEvent(new GatewayEvent(
        id: 'smtp-event-2',
        protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION,
        route: 'unknown@example.test',
        payload: 'message',
    ));

    expect($decision->action)->toBe(GatewayAction::REJECT)
        ->and($decision->statusCode)->toBe(550);
});

it('uses the WebProxy reply destination as the authorized Webstream source', function (): void {
    $planner = new RegistryProtocolPlanner(new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::relay(reply: new Delivery(
                url: 'https://events.example.test/stream',
                subscriberId: 'stream-source',
                transport: new HttpTransport(httpProxy: 'http://proxy.example.test:8080'),
            ));
        }
    });

    $decision = $planner->planEvent(new GatewayEvent(
        id: 'stream-1',
        protocol: Protocol::WEBSTREAM,
        kind: EventKind::CONNECT,
        route: '/hooks/updates',
        host: 'gateway.example.test',
        headers: ['Last-Event-ID' => ['event-42']],
    ));

    expect($decision->action)->toBe(GatewayAction::STREAM)
        ->and($decision->stream?->target)->toBe('https://events.example.test/stream')
        ->and($decision->stream?->subscriberId)->toBe('stream-source')
        ->and($decision->stream?->headers['Last-Event-ID'])->toBe(['event-42'])
        ->and($decision->stream?->transport?->httpProxy)->toBe('http://proxy.example.test:8080');
});
