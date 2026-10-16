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
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionGrant;
use Webong\Gateway\Email\MailboxAuthenticator;
use Webong\Gateway\Email\MailboxAccessGrant;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;

it('requires route opt-in for metadata-only uploads and idempotent retries', function (bool $upload, bool $enabled, string $key, int $status): void {
    $route = new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::relay(reply: new Delivery(url: 'https://source.test/events'));
        }
    };
    $policies = app(WebstreamRoutePolicyStore::class);
    if ($enabled) {
        $policies->create(['host' => 'gateway.test', 'path' => '/events',
            'source' => 'https://source.test/events', 'upload' => true, 'retry' => true]);
    }
    $planner = new RegistryProtocolPlanner($route, webstreamRoutePolicies: $policies);
    $decision = $planner->planEvent(new GatewayEvent(
        id: 'request', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT, route: '/events', host: 'gateway.test', method: 'POST',
        headers: $key !== '' ? ['idempotency-key' => [$key]] : [], attributes: $upload ? ['upload_mode' => 'stream'] : [], payload: $upload ? '' : 'input',
    ));
    if ($status !== 200) {
        expect($decision->statusCode)->toBe($status)->and($decision->action)->toBe(GatewayAction::REJECT);
        return;
    }
    expect($decision->action)->toBe(GatewayAction::STREAM)
        ->and($decision->stream->attributes['upload_mode'])->toBe($upload ? 'stream' : 'buffered')
        ->and($decision->stream->attributes['retry_idempotent'])->toBe(! $upload && $enabled ? 'true' : 'false');
    if (! $upload && $enabled) { expect($decision->stream->headers['Idempotency-Key'])->toBe([$key]); }
})->with([
    [true, false, '', 403], [true, true, '', 200], [false, true, '', 400], [false, true, 'key-1', 200], [false, false, '', 200],
]);

it('does not route SMTP credentials through registry subscriptions', function (): void {
    $adapter = new class implements RoutePlanner {
        public bool $called = false;

        public function plan(IngressRequest $request): RoutePlan
        {
            $this->called = true;

            throw new RuntimeException('AUTH was sent to the registry.');
        }
    };
    $decision = (new RegistryProtocolPlanner($adapter))->planEvent(new GatewayEvent(
        id: 'smtp-auth-1',
        protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE,
        route: 'subscriber',
        attributes: ['username' => 'subscriber', 'password' => 'secret'],
    ));

    expect($adapter->called)->toBeFalse()
        ->and($decision->action)->toBe(GatewayAction::REJECT);
});

it('uses a deployment-owned authenticator to grant exact SMTP senders', function (): void {
    $adapter = new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            throw new RuntimeException('AUTH was sent to the registry.');
        }
    };
    $authenticator = new class implements EmailSubmissionAuthenticator {
        public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
        {
            return ($event->attributes['username'] ?? '') === 'subscriber'
                && ($event->attributes['password'] ?? '') === 'secret'
                ? new EmailSubmissionGrant('account-123', ['sender@example.test']) : null;
        }
    };
    $planner = new RegistryProtocolPlanner($adapter, submissionAuthenticator: $authenticator);
    $event = new GatewayEvent(
        id: 'smtp-auth-2',
        protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE,
        route: 'subscriber',
        attributes: ['username' => 'subscriber', 'password' => 'secret'],
    );
    $accepted = $planner->planEvent($event);
    $rejected = $planner->planEvent(new GatewayEvent(
        id: 'smtp-auth-3',
        protocol: Protocol::SMTP,
        kind: EventKind::AUTHENTICATE,
        route: 'subscriber',
        attributes: ['username' => 'subscriber', 'password' => 'wrong'],
    ));

    expect($accepted->action)->toBe(GatewayAction::ACCEPT)
        ->and($accepted->metadata['auth_identity'])->toBe('account-123')
        ->and($accepted->metadata['authorized_mail_from'])->toBe('["sender@example.test"]')
        ->and($rejected->action)->toBe(GatewayAction::REJECT);
});

it('keeps POP3 and IMAP credentials out of registry routes and accepts only configured mailbox grants', function (): void {
    $routePlanner = new class implements RoutePlanner {
        public bool $called = false;

        public function plan(IngressRequest $request): RoutePlan
        {
            $this->called = true;

            throw new RuntimeException('Mailbox credential reached the registry.');
        }
    };
    $authenticator = new class implements MailboxAuthenticator {
        public function authorize(GatewayEvent $event): ?MailboxAccessGrant
        {
            return ($event->attributes['password'] ?? '') === 'secret'
                ? new MailboxAccessGrant('account-123', 'primary', 'upstream-alice', 'upstream-secret') : null;
        }
    };
    $planner = new RegistryProtocolPlanner($routePlanner, mailboxAuthenticator: $authenticator);
    foreach ([Protocol::POP3, Protocol::IMAP] as $protocol) {
        $event = new GatewayEvent(
            id: $protocol->value.'-auth-1', protocol: $protocol, kind: EventKind::AUTHENTICATE,
            route: 'alice@example.test', attributes: ['username' => 'alice@example.test', 'password' => 'secret'],
        );
        $denied = (new RegistryProtocolPlanner($routePlanner))->planEvent($event);
        $accepted = $planner->planEvent($event);
        $connected = $planner->planEvent(new GatewayEvent(
            id: $protocol->value.'-connect-1', protocol: $protocol, kind: EventKind::CONNECT, route: 'alice@example.test',
        ));

        expect($routePlanner->called)->toBeFalse()
            ->and($denied->action)->toBe(GatewayAction::REJECT)
            ->and($accepted->protocol)->toBe($protocol)
            ->and($accepted->action)->toBe(GatewayAction::ACCEPT)
            ->and($accepted->metadata['upstream_id'])->toBe('primary')
            ->and($accepted->metadata['upstream_username'])->toBe('upstream-alice')
            ->and($accepted->metadata['upstream_password'])->toBe('upstream-secret')
            ->and($connected->action)->toBe(GatewayAction::ACCEPT);
    }
});

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

it('uses the Proxy reply destination as the authorized Webstream source', function (string $resumeHeader): void {
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
        headers: [$resumeHeader => ['event-42']],
    ));

    expect($decision->action)->toBe(GatewayAction::STREAM)
        ->and($decision->stream?->target)->toBe('https://events.example.test/stream')
        ->and($decision->stream?->subscriberId)->toBe('stream-source')
        ->and($decision->stream?->headers['Last-Event-ID'])->toBe(['event-42'])
        ->and($decision->stream?->transport?->httpProxy)->toBe('http://proxy.example.test:8080');
})->with(['Last-Event-ID', 'Last-Event-Id', 'last-event-id']);

it('authorizes POST stream bodies without forwarding client credentials', function (): void {
    $planner = new RegistryProtocolPlanner(new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            expect($request->method)->toBe('POST')->and($request->body)->toBe('{"input":"hello"}');
            return RoutePlan::relay(reply: new Delivery(url: 'https://events.example.test/stream'));
        }
    });
    $decision = $planner->planEvent(new GatewayEvent(
        id: 'post-stream', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT,
        method: 'POST', route: '/events', rawQuery: 'model=test',
        headers: ['content-type' => ['application/json'], 'Authorization' => ['client-secret'], 'Cookie' => ['private']],
        payload: '{"input":"hello"}',
    ));
    expect($decision->stream?->attributes['method'])->toBe('POST')
        ->and($decision->stream?->attributes['raw_query'])->toBe('model=test')
        ->and($decision->stream?->payload)->toBe('{"input":"hello"}')
        ->and($decision->stream?->headers)->toBe(['Content-Type' => ['application/json']]);
});

it('rejects POST streams when the selected source requires GET', function (): void {
    $planner = new RegistryProtocolPlanner(new class implements RoutePlanner {
        public function plan(IngressRequest $request): RoutePlan
        {
            return RoutePlan::relay(reply: new Delivery(url: 'https://events.example.test/stream', method: 'GET'));
        }
    });
    $decision = $planner->planEvent(new GatewayEvent(id: 'post', protocol: Protocol::WEBSTREAM, kind: EventKind::CONNECT, route: '/events', method: 'POST'));
    expect($decision->action)->toBe(GatewayAction::REJECT)->and($decision->statusCode)->toBe(405);
});
