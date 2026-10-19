<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Database\QueryException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Resources\PlannerResourceStore;

it('creates a service-neutral account that can own resources from different kinds', function (): void {
    $this->withToken('registry-secret')->postJson('/accounts', ['id' => 'client-a'])->assertForbidden();
    $this->withHeader('Authorization', '')->postJson('https://localhost/accounts', ['id' => 'client-a'])->assertUnauthorized();
    $response = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'client-a'])
        ->assertCreated();
    expect($response->headers->get('Cache-Control'))->toContain('no-store');
    $token = $response->json('token');
    $accounts = app(AccountStore::class);
    $resources = app(PlannerResourceStore::class);
    $web = $resources->create('web.endpoint', 'events', ['route' => '/events'], true, owner: 'client-a');
    $dns = $resources->create('dns.zone', 'example.test', ['zone' => 'example.test'], true, owner: 'client-a');
    expect(fn () => $resources->create('dns.zone', 'missing.test', [], true, owner: 'missing-account'))
        ->toThrow(QueryException::class);

    expect($accounts->authorized('client-a', $token))->toBeTrue()
        ->and($accounts->authorized('client-b', $token))->toBeFalse()
        ->and(DB::table('accounts')->where('id', 'client-a')->value('token_hash'))->toBe(hash('sha256', $token))
        ->and($resources->findOwned('web.endpoint', $web->id, 'client-a'))->not->toBeNull()
        ->and($resources->findOwned('dns.zone', $dns->id, 'client-a'))->not->toBeNull()
        ->and($resources->findOwned('web.endpoint', $web->id, 'client-b'))->toBeNull();
    $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'client-a'])->assertConflict();
});
