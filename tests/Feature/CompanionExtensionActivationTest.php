<?php

declare(strict_types=1);

use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Tests\Fixtures\CreateTicketCompanionAction;
use Webong\Gateway\Tests\Fixtures\FakeDynamicCompanionProvider;

function extensionInvocation(string $execution = 'extension-execution-1'): CompanionActionInvocation
{
    return new CompanionActionInvocation(
        action: 'test.switch-provider',
        executionId: $execution,
        idempotencyKey: $execution,
        input: ['subject' => 'Switch me'],
        deadline: new DateTimeImmutable('+30 seconds'),
        companionVersion: '1.0.0',
    );
}

beforeEach(function (): void {
    CreateTicketCompanionAction::$calls = 0;
    $registry = app(CompanionActionRegistry::class);
    $registry->register(
        'test.switch-provider',
        CreateTicketCompanionAction::class,
        version: '1.0.0',
        extension: 'example/switch-provider',
    );
    $registry->registerProvider(new FakeDynamicCompanionProvider(
        'test.switch-provider',
        'example/switch-provider',
    ));
});

it('keeps existing extensions static until Planner enables the dynamic candidate', function (): void {
    $registry = app(CompanionActionRegistry::class);

    expect($registry->capability('test.switch-provider')?->mode)->toBe('static')
        ->and($registry->capability('test.switch-provider')?->generation)->toBe(0)
        ->and($registry->invoke(extensionInvocation())->output['ticket_id'])->toBe('ticket-1')
        ->and(CreateTicketCompanionAction::$calls)->toBe(1);

    $activation = app(CompanionActionActivationStore::class)->create([
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'enabled' => true,
    ]);

    $capability = $registry->capability('test.switch-provider');
    expect($capability?->mode)->toBe('dynamic')
        ->and($capability?->generation)->toBe(1)
        ->and($registry->invoke(extensionInvocation('extension-execution-2'))->output)->toBe([
            'provider' => 'dynamic',
            'subject' => 'Switch me',
        ])
        ->and(CreateTicketCompanionAction::$calls)->toBe(1);

    app(CompanionActionActivationStore::class)->replace($activation->id, [
        'extension' => 'example/switch-provider',
        'mode' => 'static',
        'version' => '1.0.0',
        'enabled' => true,
    ], $activation->revision);

    expect($registry->capability('test.switch-provider')?->mode)->toBe('static')
        ->and($registry->invoke(extensionInvocation('extension-execution-3'))->output['ticket_id'])->toBe('ticket-2')
        ->and(CreateTicketCompanionAction::$calls)->toBe(2);
});

it('makes repeated discovery idempotent but refuses two different providers for one mode', function (): void {
    $registry = app(CompanionActionRegistry::class);
    $same = new FakeDynamicCompanionProvider('test.switch-provider', 'example/switch-provider');
    $registry->registerProvider($same);
    $registry->registerProvider($same);

    expect(fn () => $registry->registerProvider(new FakeDynamicCompanionProvider(
        'test.switch-provider',
        'example/switch-provider',
        id: 'dynamic:other',
    )))->toThrow(InvalidArgumentException::class);
});

it('fails closed when the selected provider is disabled, absent or version incompatible', function (): void {
    $registry = app(CompanionActionRegistry::class);
    $store = app(CompanionActionActivationStore::class);
    $registry->register(
        'test.static-only',
        CreateTicketCompanionAction::class,
        version: '1.0.0',
        extension: 'example/static-only',
    );
    $store->create([
        'extension' => 'example/static-only',
        'mode' => 'dynamic',
    ]);
    $absent = collect($registry->statuses())->firstWhere('extension', 'example/static-only');
    expect($registry->capability('test.static-only'))->toBeNull()
        ->and($absent['state'])->toBe('unavailable');

    $row = $store->create([
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '2.0.0',
    ]);
    $status = collect($registry->statuses())->firstWhere('extension', 'example/switch-provider');

    expect($registry->capability('test.switch-provider'))->toBeNull()
        ->and($status['state'])->toBe('incompatible');

    $store->replace($row->id, [
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'enabled' => false,
    ], $row->revision);
    $status = collect($registry->statuses())->firstWhere('extension', 'example/switch-provider');

    expect($registry->capability('test.switch-provider'))->toBeNull()
        ->and($status['state'])->toBe('disabled');
});

it('exposes extension activation and runtime status through the authenticated Planner CMS', function (): void {
    $this->getJson('/extensions')->assertUnauthorized();
    $created = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'enabled' => true,
    ])->assertCreated()
        ->assertJsonPath('extension', 'example/switch-provider')
        ->assertJsonPath('mode', 'dynamic');

    $this->withToken('registry-secret')->getJson('/extensions')
        ->assertOk()
        ->assertJsonFragment([
            'extension' => 'example/switch-provider',
            'state' => 'ready',
            'mode' => 'dynamic',
        ]);

    $this->withToken('registry-secret')->getJson('/extensions/'.$created->json('id'))
        ->assertOk()
        ->assertJsonPath('revision', 1);
});

it('stores a pinned extension artifact as Planner-owned desired state', function (): void {
    $created = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'artifact' => [
            'sha256' => str_repeat('a', 64),
            'url' => 'https://extensions.example.test/switch-provider-1.0.0.tar.gz',
        ],
    ])->assertCreated()
        ->assertJsonPath('artifact.url', 'https://extensions.example.test/switch-provider-1.0.0.tar.gz')
        ->assertJsonPath('artifact.sha256', str_repeat('a', 64));

    $selection = app(CompanionActionActivationStore::class)
        ->selection('example/switch-provider');

    expect($selection->artifact)->toBe([
        'url' => 'https://extensions.example.test/switch-provider-1.0.0.tar.gz',
        'sha256' => str_repeat('a', 64),
    ]);

    $this->withToken('registry-secret')->getJson('/extensions/'.$created->json('id'))
        ->assertOk()
        ->assertJsonPath('artifact.sha256', str_repeat('a', 64));
});

it('rejects unpinned, non-HTTPS or static extension artifacts', function (array $input): void {
    $this->withToken('registry-secret')->postJson('/extensions', $input)
        ->assertUnprocessable();
})->with([
    'missing version' => [[
        'extension' => 'example/artifact',
        'mode' => 'dynamic',
        'artifact' => [
            'url' => 'https://extensions.example.test/artifact.tar.gz',
            'sha256' => str_repeat('a', 64),
        ],
    ]],
    'http URL' => [[
        'extension' => 'example/artifact',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'artifact' => [
            'url' => 'http://extensions.example.test/artifact.tar.gz',
            'sha256' => str_repeat('a', 64),
        ],
    ]],
    'invalid digest' => [[
        'extension' => 'example/artifact',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'artifact' => [
            'url' => 'https://extensions.example.test/artifact.tar.gz',
            'sha256' => 'not-a-digest',
        ],
    ]],
    'static artifact' => [[
        'extension' => 'example/artifact',
        'mode' => 'static',
        'version' => '1.0.0',
        'artifact' => [
            'url' => 'https://extensions.example.test/artifact.tar.gz',
            'sha256' => str_repeat('a', 64),
        ],
    ]],
]);

it('serves enabled extension artifact desired state only to private plane reconcilers', function (): void {
    app(CompanionActionActivationStore::class)->create([
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'digest' => str_repeat('b', 64),
        'artifact' => [
            'url' => 'https://extensions.example.test/switch-provider-1.0.0.tar.gz',
            'sha256' => str_repeat('a', 64),
        ],
    ]);
    app(CompanionActionActivationStore::class)->create([
        'extension' => 'example/disabled-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'enabled' => false,
        'artifact' => [
            'url' => 'https://extensions.example.test/disabled-provider-1.0.0.tar.gz',
            'sha256' => str_repeat('c', 64),
        ],
    ]);

    $this->getJson('/_internal/gateway/extensions')->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions')
        ->assertOk()
        ->assertExactJson(['data' => [[
            'extension' => 'example/switch-provider',
            'version' => '1.0.0',
            'digest' => str_repeat('b', 64),
            'artifact' => [
                'url' => 'https://extensions.example.test/switch-provider-1.0.0.tar.gz',
                'sha256' => str_repeat('a', 64),
            ],
            'revision' => 1,
        ]]]);
});

it('owns bounded warmed companion execution policy through Planner', function (): void {
    $created = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'example/switch-provider',
        'mode' => 'dynamic',
        'version' => '1.0.0',
        'enabled' => true,
        'execution' => [
            'mode' => 'warmed',
            'max_invocations' => 25,
            'max_age_seconds' => 120,
            'idle_timeout_seconds' => 15,
        ],
    ])->assertCreated()
        ->assertJsonPath('execution.mode', 'warmed')
        ->assertJsonPath('execution.max_invocations', 25)
        ->assertJsonPath('execution.max_age_seconds', 120)
        ->assertJsonPath('execution.idle_timeout_seconds', 15);

    $this->withToken('registry-secret')->getJson('/extensions')
        ->assertOk()
        ->assertJsonFragment([
            'extension' => 'example/switch-provider',
            'execution' => [
                'mode' => 'warmed',
                'max_invocations' => 25,
                'max_age_seconds' => 120,
                'idle_timeout_seconds' => 15,
            ],
        ]);

    expect(app(CompanionActionActivationStore::class)
        ->selection('example/switch-provider')->execution->generation)->toBe(1);
    expect($created->json('revision'))->toBe(1);
});

it('rejects invalid or static warmed companion execution policies', function (array $execution, string $mode = 'dynamic'): void {
    $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'example/invalid-execution',
        'mode' => $mode,
        'execution' => $execution,
    ])->assertUnprocessable();
})->with([
    'old warm spelling' => [['mode' => 'warm']],
    'string bound' => [['mode' => 'warmed', 'max_invocations' => '10']],
    'idle above age' => [['mode' => 'warmed', 'max_age_seconds' => 10, 'idle_timeout_seconds' => 11]],
    'warmed static provider' => [['mode' => 'warmed'], 'static'],
]);
