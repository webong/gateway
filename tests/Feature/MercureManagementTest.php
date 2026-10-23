<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Servers\ServerTables;

it('creates and manages an encrypted Mercure hub', function (): void {
    $server = $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'mercure',
        'name' => 'customer-updates',
        'hostname' => 'updates.example.test',
        'driver' => 'docker',
        'configuration' => [
            'publisher_jwt' => ['key' => 'publisher-private-key', 'algorithm' => 'HS256'],
            'subscriber_jwt' => ['key' => 'subscriber-private-key', 'algorithm' => 'HS256'],
            'anonymous' => true,
            'cors_origins' => ['https://app.example.test'],
            'subscriptions' => true,
        ],
    ])->assertCreated()
        ->assertJsonPath('desired_state', 'stopped')
        ->assertJsonPath('configuration.publisher_jwt.key_configured', true)
        ->assertJsonMissing(['key' => 'publisher-private-key'])
        ->json();

    expect(DB::table(ServerTables::servers())->value('type'))->toBe('mercure')
        ->and(DB::table(ServerTables::servers())->value('configuration'))->not->toContain('publisher-private-key');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/provisioning/servers')
        ->assertJsonPath('data.0.type', 'mercure')
        ->assertJsonPath('data.0.configuration.publisher_jwt_key', 'publisher-private-key');

    $this->withToken('registry-secret')->postJson("/servers/{$server['id']}/start")
        ->assertAccepted()->assertJsonPath('desired_state', 'running');

    $this->withToken('registry-secret')->postJson('/applications', ['server_id' => $server['id']])
        ->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('/mercure/servers', [])->assertNotFound();
});

it('rejects unsafe Mercure Community replication', function (): void {
    $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'mercure',
        'name' => 'clustered-hub',
        'hostname' => 'clustered.example.test',
        'replicas' => 2,
        'configuration' => [
            'publisher_jwt' => ['key' => 'publisher-key'],
            'subscriber_jwt' => ['key' => 'subscriber-key'],
        ],
    ])->assertUnprocessable()->assertJsonValidationErrors('replicas');
});
