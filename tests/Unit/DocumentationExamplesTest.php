<?php

declare(strict_types=1);

use Webong\Gateway\Protocols\MatchRules;

it('keeps remote subscription examples aligned with the Planner DSL', function (
    string $filename,
    ?string $expectedProfile,
): void {
    $payload = json_decode(
        file_get_contents(dirname(__DIR__, 2).'/docs/examples/subscriptions/'.$filename),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );

    expect($payload)->toHaveKeys([
        'subscriber_id',
        'subscription_id',
        'type',
        'webhook_group',
        'routing_scope',
        'routing_key',
        'url',
        'match',
    ])->and($payload['profile'] ?? null)->toBe($expectedProfile)
        ->and(MatchRules::fromArray($payload['match'])->toArray())->toBe($payload['match']);
})->with([
    'default transport' => ['remote-direct.json', null],
    'named endpoint profile' => ['remote-profile.json', 'proxy'],
]);
