<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\IngressRequest;
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
        attributes: ['mail_from' => 'sender@example.test'],
        payload: "Subject: hello\r\n\r\nmessage",
    ));

    expect($adapter->received)->toBeInstanceOf(IngressRequest::class)
        ->and($adapter->received->protocol)->toBe('smtp')
        ->and($adapter->received->event)->toBe('transaction')
        ->and($adapter->received->body)->toBe("Subject: hello\r\n\r\nmessage")
        ->and($decision->protocol)->toBe(Protocol::SMTP)
        ->and($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries[0]->protocol)->toBe(Protocol::HTTP)
        ->and($decision->deliveries[0]->subscriberId)->toBe('subscriber-1')
        ->and($decision->deliveries[0]->payload)->toBe("Subject: hello\r\n\r\nmessage");
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
