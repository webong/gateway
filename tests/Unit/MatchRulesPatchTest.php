<?php

declare(strict_types=1);

use Webong\NetGateway\Protocol\MatchRules;
use Webong\NetGateway\Protocol\MatchRulesPatch;

it('applies ordered add and remove operations incrementally', function (): void {
    $current = MatchRules::make([
        'headers.X-Old-Event' => ['present'],
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ]);

    $patch = MatchRulesPatch::make()
        ->remove('body.account.id', 'in:account-42')
        ->add('body.account.id', ['string', 'in:account-99'])
        ->remove('headers.X-Old-Event');

    expect($patch->apply($current)->toArray())->toBe([
        'version' => 'v1',
        'rules' => [
            'body.account.id' => ['required', 'string', 'in:account-99'],
        ],
    ])->and($patch->toArray())->toBe([
        'version' => 'v1',
        'operations' => [
            ['op' => 'remove', 'field' => 'body.account.id', 'rules' => ['in:account-42']],
            ['op' => 'add', 'field' => 'body.account.id', 'rules' => ['string', 'in:account-99']],
            ['op' => 'remove', 'field' => 'headers.x-old-event'],
        ],
    ]);
});

it('accepts canonical JSON operations and keeps removals idempotent', function (): void {
    $patch = MatchRulesPatch::fromArray([
        'version' => 'v1',
        'operations' => [
            ['op' => 'remove', 'field' => 'query.missing'],
            ['op' => 'add', 'field' => 'body.kind', 'rules' => 'required|in:message'],
        ],
    ]);

    expect($patch->apply(MatchRules::any())->rules)->toBe([
        'body.kind' => ['required', 'in:message'],
    ]);
});

it('rejects applying an empty fluent patch', function (): void {
    expect(fn (): MatchRules => MatchRulesPatch::make()->apply(MatchRules::any()))
        ->toThrow(InvalidArgumentException::class, 'requires at least one operation');
});

it('rejects unsafe rules and malformed operations', function (array $payload, string $message): void {
    expect(fn (): MatchRulesPatch => MatchRulesPatch::fromArray($payload))
        ->toThrow(InvalidArgumentException::class, $message);
})->with([
    'unsafe rule' => [[
        'version' => 'v1',
        'operations' => [[
            'op' => 'add',
            'field' => 'body.account.id',
            'rules' => ['exists:accounts,id'],
        ]],
    ], 'Match rule [exists] is not allowed.'],
    'missing add rules' => [[
        'version' => 'v1',
        'operations' => [[
            'op' => 'add',
            'field' => 'body.account.id',
        ]],
    ], 'Add operations require rules.'],
    'unknown operation' => [[
        'version' => 'v1',
        'operations' => [[
            'op' => 'replace',
            'field' => 'body.account.id',
            'rules' => ['required'],
        ]],
    ], 'Unsupported match-patch operation [replace].'],
]);
