<?php

declare(strict_types=1);

use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\MatchRules;
use Webong\WebRelay\RequestMatcher;
use Webong\WebRelay\SubscriptionMatcher;

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
