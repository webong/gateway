<?php

declare(strict_types=1);

use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\RequestMatcher;
use Webong\Gateway\SubscriptionMatcher;

it('matches normalized headers and JSON body using Laravel rules', function (): void {
    $request = new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: 'source=meta',
        headers: ['X-Event-Type' => ['message.created']],
        body: '{"account":{"id":"account-42"}}',
    );
    $match = MatchRules::make([
        'headers.X-Event-Type' => ['required', 'in:message.created'],
        'query.source' => ['required', 'in:meta'],
        'body.account.id' => ['required', 'in:account-42'],
        'route' => ['required', 'in:/provider/events/app-123'],
        'payload.account.id' => ['required', 'in:account-42'],
    ]);

    expect(app(RequestMatcher::class)->matches($request, $match))->toBeTrue();
});

it('keeps all values when header casing differs', function (): void {
    $request = new IngressRequest(
        deliveryId: 'delivery-headers',
        method: 'POST',
        host: 'relay.example.test',
        path: '/events',
        rawQuery: '',
        headers: [
            'X-Event-Type' => ['message.created'],
            'x-event-type' => ['message.updated'],
        ],
        body: '',
    );
    $matcher = app(RequestMatcher::class);

    expect($matcher->matchingDocument($request)['headers']['x-event-type'])
        ->toBe(['message.created', 'message.updated'])
        ->and($matcher->matches($request, MatchRules::make([
            'headers.x-event-type' => ['array', 'size:2'],
        ])))->toBeTrue();
});

it('requires a wildcard parent explicitly when at least one item must exist', function (): void {
    $request = new IngressRequest(
        deliveryId: 'delivery-empty-items',
        method: 'POST',
        host: 'relay.example.test',
        path: '/events',
        rawQuery: '',
        headers: [],
        body: '{"items":[]}',
    );
    $matcher = app(RequestMatcher::class);

    expect($matcher->matches($request, MatchRules::make([
        'payload.items.*.type' => ['required', 'string'],
    ])))->toBeTrue()
        ->and($matcher->matches($request, MatchRules::make([
            'payload.items' => ['required', 'array', 'min:1'],
            'payload.items.*.type' => ['required', 'string'],
        ])))->toBeFalse();
});

it('fails closed when persisted rules are malformed', function (): void {
    $request = new IngressRequest(
        deliveryId: 'delivery-1',
        method: 'POST',
        host: 'relay.example.test',
        path: '/provider/events/app-123',
        rawQuery: '',
        headers: [],
        body: '{}',
    );

    expect(app(SubscriptionMatcher::class)->matches($request, [
        MatchRules::METADATA_KEY => [
            'version' => 'v1',
            'rules' => ['body.account_id' => ['exists:accounts,id']],
        ],
    ]))->toBeFalse()
        ->and(app(SubscriptionMatcher::class)->matches($request, [
            MatchRules::METADATA_KEY => [
                'version' => 'v1',
                'rules' => ['body.account_id' => ['max:1e999']],
            ],
        ]))->toBeFalse();
});

it('matches DNS event fields without exposing synthetic HTTP fields', function (): void {
    $request = new IngressRequest(
        deliveryId: 'dns-delivery-1',
        method: 'POST',
        host: 'payload.hook-1.dns.example.test.',
        path: '/hook-1',
        rawQuery: '',
        headers: ['Content-Type' => ['application/json']],
        body: '{"data":"payload"}',
        scheme: 'dns',
        protocol: 'dns',
        event: 'query',
        attributes: ['qtype' => 'TXT', 'data' => 'payload'],
    );
    $match = MatchRules::make([
        'protocol' => ['required', 'in:dns'],
        'event' => ['required', 'in:query'],
        'route' => ['required', 'in:/hook-1'],
        'attributes.qtype' => ['required', 'in:TXT'],
        'attributes.data' => ['required', 'starts_with:pay'],
        'payload.data' => ['required', 'in:payload'],
    ]);

    $matcher = app(RequestMatcher::class);

    expect($matcher->matches($request, $match))->toBeTrue()
        ->and($matcher->matchingDocument($request))->not->toHaveKeys(['method', 'scheme', 'path', 'query', 'body'])
        ->and($matcher->matches($request, MatchRules::make(['method' => ['present']])))->toBeFalse()
        ->and($matcher->matches($request, MatchRules::make(['method' => ['nullable', 'in:POST']])))->toBeFalse();
});

it('matches SMTP route, headers, attributes and raw payload', function (): void {
    $request = new IngressRequest(
        deliveryId: 'smtp-delivery-1',
        method: 'POST',
        host: 'mail.example.test',
        path: 'inbox@example.test',
        rawQuery: '',
        headers: ['Subject' => ['New message']],
        body: "Subject: New message\r\n\r\nHello",
        scheme: 'smtp',
        protocol: 'smtp',
        event: 'transaction',
        attributes: ['mail_from' => 'sender@example.test'],
    );
    $matcher = app(RequestMatcher::class);

    expect($matcher->matches($request, MatchRules::make([
        'protocol' => ['in:smtp'],
        'event' => ['in:transaction'],
        'route' => ['in:inbox@example.test'],
        'headers.subject' => ['in:New message'],
        'attributes.mail_from' => ['in:sender@example.test'],
        'payload' => ['starts_with:Subject: New message'],
    ])))->toBeTrue()
        ->and($matcher->matchingDocument($request))->not->toHaveKeys(['method', 'scheme', 'path', 'query', 'body']);
});

it('matches WebSocket message events without treating the upgrade as an HTTP request', function (): void {
    $request = new IngressRequest(
        deliveryId: 'websocket-delivery-1',
        method: 'GET',
        host: 'ws.example.test',
        path: '/chat',
        rawQuery: 'room=one',
        headers: [],
        body: '{"kind":"ping"}',
        protocol: 'websocket',
        event: 'message',
        sessionId: 'session-1',
        attributes: ['message_type' => 'text'],
    );
    $matcher = app(RequestMatcher::class);

    expect($matcher->matches($request, MatchRules::make([
        'protocol' => ['in:websocket'],
        'event' => ['in:message'],
        'session_id' => ['in:session-1'],
        'route' => ['in:/chat'],
        'attributes.message_type' => ['in:text'],
        'payload.kind' => ['in:ping'],
    ])))->toBeTrue()
        ->and($matcher->matchingDocument($request))->not->toHaveKeys(['method', 'scheme', 'path', 'query', 'body']);
});
