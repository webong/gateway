<?php

declare(strict_types=1);

use Webong\NetGateway\Protocol\MatchRules;

it('normalizes PHP rules to the canonical HTTP representation', function (): void {
    $rules = MatchRules::make([
        'headers.X-Event-Type' => 'required|in:message.created,message.updated',
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ]);

    expect($rules->toArray())->toBe([
        'version' => 'v1',
        'rules' => [
            'headers.x-event-type' => ['required', 'in:message.created,message.updated'],
            'body.account.id' => ['required', 'string', 'in:account-42'],
        ],
    ]);
});

it('rejects unsafe remote rules and fields', function (array $payload): void {
    expect(fn (): MatchRules => MatchRules::fromArray($payload))
        ->toThrow(InvalidArgumentException::class);
})->with([
    'database rule' => [[
        'version' => 'v1',
        'rules' => ['body.account_id' => ['exists:accounts,id']],
    ]],
    'regular expression' => [[
        'version' => 'v1',
        'rules' => ['body.event' => ['regex:/message/']],
    ]],
    'unbounded field' => [[
        'version' => 'v1',
        'rules' => ['application.secret' => ['required']],
    ]],
    'missing protocol version' => [[
        'rules' => ['body.event' => ['required']],
    ]],
    'unknown protocol property' => [[
        'version' => 'v1',
        'rules' => ['body.event' => ['required']],
        'handler' => 'arbitrary-code',
    ]],
]);
