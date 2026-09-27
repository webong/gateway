<?php

declare(strict_types=1);

use Webong\Gateway\Protocol\MatchRules;

it('normalizes PHP rules to the canonical JSON representation', function (): void {
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

it('accepts protocol-neutral event, route, payload and attribute fields', function (): void {
    $rules = MatchRules::make([
        'protocol' => ['required', 'in:dns'],
        'event' => ['required', 'in:query'],
        'route' => ['required', 'in:/hook-1'],
        'payload.data' => ['required', 'string'],
        'attributes.qtype' => ['required', 'in:A,TXT'],
    ]);

    expect($rules->toArray()['rules'])->toHaveKeys(['protocol', 'event', 'route', 'payload.data', 'attributes.qtype']);
});

it('keeps valid bounded parameterized rules available', function (): void {
    $rules = MatchRules::make([
        'payload.items' => ['required', 'array', 'between:1,5'],
        'payload.score' => ['numeric', 'min:-10', 'max:10'],
        'protocol' => ['in:http,smtp'],
        'payload.kind' => ['in:"message,created"'],
    ]);

    expect($rules->rules['payload.items'])->toBe(['required', 'array', 'between:1,5'])
        ->and($rules->rules['payload.score'])->toBe(['numeric', 'min:-10', 'max:10'])
        ->and($rules->rules['payload.kind'])->toBe(['in:"message,created"']);
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
    'invalid required arguments' => [[
        'version' => 'v1',
        'rules' => ['protocol' => ['required:ignored']],
    ]],
    'missing between upper bound' => [[
        'version' => 'v1',
        'rules' => ['payload.size' => ['between:1']],
    ]],
    'reversed between bounds' => [[
        'version' => 'v1',
        'rules' => ['payload.size' => ['between:5,1']],
    ]],
    'nonnumeric size' => [[
        'version' => 'v1',
        'rules' => ['payload.size' => ['min:many']],
    ]],
    'infinite size' => [[
        'version' => 'v1',
        'rules' => ['payload.size' => ['max:1e999']],
    ]],
    'extreme exponent' => [[
        'version' => 'v1',
        'rules' => ['payload.size' => ['min:1e-999999999']],
    ]],
    'empty in-list' => [[
        'version' => 'v1',
        'rules' => ['protocol' => ['in:']],
    ]],
    'empty list item' => [[
        'version' => 'v1',
        'rules' => ['protocol' => ['in:http,,smtp']],
    ]],
    'empty array key' => [[
        'version' => 'v1',
        'rules' => ['payload' => ['array:kind,']],
    ]],
    'associative rule list' => [[
        'version' => 'v1',
        'rules' => ['protocol' => ['one' => 'required']],
    ]],
    'normalized header collision' => [[
        'version' => 'v1',
        'rules' => [
            'headers.X-Event' => ['in:first'],
            'headers.x-event' => ['in:second'],
        ],
    ]],
    'oversized field name' => [[
        'version' => 'v1',
        'rules' => ['payload.'.str_repeat('a', 513) => ['required']],
    ]],
]);
