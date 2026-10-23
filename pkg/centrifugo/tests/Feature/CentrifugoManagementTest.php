<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Webong\Gateway\Servers\ServerTables;

it('creates a scalable encrypted Centrifugo server', function (): void {
    $server = $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'centrifugo',
        'name' => 'customer-events',
        'hostname' => 'events.example.test',
        'driver' => 'kubernetes',
        'replicas' => 3,
        'configuration' => [
            'client' => [
                'token_hmac_secret_key' => 'client-token-secret',
                'allowed_origins' => ['https://app.example.test'],
            ],
            'channel' => ['allow_subscribe_for_client' => true],
            'http_api' => ['key' => 'http-api-secret'],
            'engine' => ['type' => 'redis', 'redis_address' => 'redis://redis.internal:6379'],
            'prometheus_enabled' => true,
        ],
    ])->assertCreated()
        ->assertJsonPath('replicas', 3)
        ->assertJsonPath('configuration.engine.type', 'redis')
        ->assertJsonPath('configuration.client.token_hmac_secret_key_configured', true)
        ->assertJsonMissing(['key' => 'http-api-secret'])
        ->json();

    expect(DB::table(ServerTables::servers())->value('type'))->toBe('centrifugo')
        ->and(DB::table(ServerTables::servers())->value('configuration'))->not->toContain('client-token-secret');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/provisioning/servers')
        ->assertJsonPath('data.0.type', 'centrifugo')
        ->assertJsonPath('data.0.configuration.http_api_key', 'http-api-secret');

    $this->withToken('registry-secret')->postJson("/servers/{$server['id']}/start")
        ->assertAccepted()->assertJsonPath('desired_state', 'running');

    $this->withToken('registry-secret')->postJson('/centrifugo/servers', [])->assertNotFound();
});

it('requires Redis for multiple Centrifugo replicas', function (): void {
    $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'centrifugo',
        'name' => 'unsafe-events',
        'hostname' => 'unsafe.example.test',
        'replicas' => 2,
        'configuration' => [
            'client' => ['token_hmac_secret_key' => 'client-secret'],
            'http_api' => ['key' => 'api-secret'],
            'engine' => ['type' => 'memory'],
        ],
    ])->assertUnprocessable()->assertJsonValidationErrors('replicas');
});
