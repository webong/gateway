<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Gateway\Resources\StaleResource;

it('stores different resource kinds in one encrypted versioned collection', function (): void {
    $store = app(PlannerResourceStore::class);
    $source = $store->create('webstream.private-source', 'same-key', ['cidrs' => ['10.0.0.0/8']]);
    $route = $store->create('webstream.route-policy', 'same-key', ['source' => 'https://source.test']);

    expect(DB::table('configurations')->count())->toBe(2)
        ->and(DB::table('configurations')->where('id', $source->id)->value('document'))
            ->not->toContain('10.0.0.0/8')
        ->and($store->byKey('webstream.route-policy', 'same-key')->id)->toBe($route->id)
        ->and($store->find('webstream.private-source', $route->id))->toBeNull();

    $changed = $store->replace('webstream.private-source', $source->id, 'new-key',
        ['cidrs' => ['10.42.0.0/16']], false, $source->revision);
    expect($changed->revision)->toBe(2)
        ->and($changed->enabled)->toBeFalse()
        ->and($store->byKey('webstream.private-source', 'same-key'))->toBeNull()
        ->and($store->byKey('webstream.private-source', 'new-key')->document['cidrs'])->toBe(['10.42.0.0/16']);

    expect(fn () => $store->replace('webstream.private-source', $source->id, 'new-key', [], true, 1))
        ->toThrow(StaleResource::class);
    expect($store->delete('webstream.private-source', $route->id, 1))->toBeFalse()
        ->and($store->delete('webstream.private-source', $source->id, $changed->revision))->toBeTrue()
        ->and($store->find('webstream.route-policy', $route->id))->not->toBeNull();
    expect(array_map(fn ($row) => $row->operation, $store->history('webstream.private-source', $source->id)))
        ->toBe(['created', 'replaced', 'deleted'])
        ->and(DB::table('configuration_revisions')->where('resource_id', $source->id)->first()->document)
        ->not->toContain('10.42.0.0/16');
});

it('exposes only registered kind names and revision metadata through the authenticated CMS API', function (): void {
    $source = app(PlannerResourceStore::class)->create('webstream.private-source', 'http://internal.test:8080',
        ['cidrs' => ['10.42.0.0/16']]);
    $this->getJson('/configurations/kinds')->assertUnauthorized();
    $kinds = $this->withToken('registry-secret')->getJson('/configurations/kinds')
        ->assertOk()->json('data');
    expect($kinds)->toContain('webstream.private-source');
    $this->withToken('registry-secret')
        ->getJson('/configurations/webstream.private-source/'.$source->id.'/revisions')
        ->assertOk()->assertJsonPath('data.0.operation', 'created')
        ->assertJsonMissing(['cidrs' => ['10.42.0.0/16']]);
    $this->withToken('registry-secret')
        ->getJson('/configurations/arbitrary-kind/'.$source->id.'/revisions')->assertNotFound();
});

it('isolates owned resources with the same key across accounts', function (): void {
    app(\Webong\Gateway\Accounts\AccountStore::class)->create('account-a');
    app(\Webong\Gateway\Accounts\AccountStore::class)->create('account-b');
    $store = app(PlannerResourceStore::class);
    $alice = $store->create('email.mailbox-connection', 'support@example.test', ['imap_upstream_id' => 'imap-a'], false,
        owner: 'account-a');
    $bob = $store->create('email.mailbox-connection', 'support@example.test', ['imap_upstream_id' => 'imap-b'], false,
        owner: 'account-b');

    expect($alice->id)->not->toBe($bob->id)
        ->and($store->findOwned('email.mailbox-connection', $alice->id, 'account-b'))->toBeNull()
        ->and($store->findOwned('email.mailbox-connection', $alice->id, 'account-a')->id)->toBe($alice->id)
        ->and($store->allOwned('email.mailbox-connection', 'account-a'))->toHaveCount(1)
        ->and($store->byOwnedKey('email.mailbox-connection', 'account-b', 'support@example.test')->id)->toBe($bob->id);
});
