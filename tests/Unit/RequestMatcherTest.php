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
    ]);

    expect(app(RequestMatcher::class)->matches($request, $match))->toBeTrue();
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
    ]))->toBeFalse();
});

it('matches protocol event attributes for DNS subscriptions', function (): void {
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
        'attributes.qtype' => ['required', 'in:TXT'],
        'attributes.data' => ['required', 'starts_with:pay'],
    ]);

    expect(app(RequestMatcher::class)->matches($request, $match))->toBeTrue();
});
