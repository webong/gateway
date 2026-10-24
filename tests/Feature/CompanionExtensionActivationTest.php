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

    app(CompanionActionActivationStore::class)->create([
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
