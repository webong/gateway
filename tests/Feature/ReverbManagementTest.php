<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;
use Laravel\Reverb\ApplicationManager;
use Laravel\Reverb\Exceptions\InvalidApplication;
use Webong\Gateway\Reverb\Models\ReverbApplication;
use Webong\Gateway\Servers\ServerTables;

function createReverbServer(array $overrides = []): array
{
    $input = [
        'type' => 'reverb',
        'name' => 'primary-reverb',
        'hostname' => 'socket.example.test',
        'path' => '/socket',
        'replicas' => 2,
        'configuration' => [
            'max_request_size' => 25000,
            'scaling' => ['enabled' => true],
        ],
    ];
    foreach ($overrides as $key => $value) {
        if (in_array($key, ['max_request_size', 'scaling', 'observability'], true)) {
            $input['configuration'][$key] = $value;
        } elseif ($key === 'configuration' && is_array($value)) {
            $input['configuration'] = [...$input['configuration'], ...$value];
        } else {
            $input[$key] = $value;
        }
    }

    return test()->withToken('registry-secret')->postJson('/servers', $input)->assertCreated()->json();
}

function createReverbApplication(string $serverId, array $overrides = []): array
{
    return test()->withToken('registry-secret')->postJson('/applications', [
        'server_id' => $serverId,
        'app_id' => 'crm-production',
        'key' => 'public-app-key',
        'secret' => 'a-private-application-secret',
        'allowed_origins' => ['crm.example.test'],
        'ping_interval' => 45,
        'activity_timeout' => 20,
        'max_message_size' => 50000,
        'max_connections' => 500,
        'accept_client_events_from' => 'members',
        'rate_limiting' => [
            'enabled' => true,
            'max_attempts' => 30,
            'decay_seconds' => 60,
            'terminate_on_limit' => false,
        ],
        'options' => ['scheme' => 'https', 'port' => 443],
        ...$overrides,
    ])->assertCreated()->json();
}

it('uses the shared configurable registry tables', function (): void {
    expect(ServerTables::servers())->toBe('test_gateway_servers')
        ->and(ServerTables::applications())->toBe('test_gateway_applications')
        ->and(ServerTables::instances())->toBe('test_gateway_instances')
        ->and(Schema::hasTable('test_gateway_servers'))->toBeTrue()
        ->and(Schema::hasTable('test_gateway_applications'))->toBeTrue()
        ->and(Schema::hasTable('test_gateway_instances'))->toBeTrue()
        ->and(Schema::hasTable('gateway_reverb_servers'))->toBeFalse();

    createReverbServer();

    expect(DB::table(ServerTables::servers())->count())->toBe(1)
        ->and(DB::table(ServerTables::servers())->value('type'))->toBe('reverb');
});

it('selects a supported runtime driver per Reverb server', function (): void {
    $docker = createReverbServer(['driver' => 'docker']);

    expect($docker['driver'])->toBe('docker');

    $this->withToken('registry-secret')
        ->patchJson("/servers/{$docker['id']}", ['driver' => 'kubernetes'])
        ->assertOk()
        ->assertJsonPath('driver', 'kubernetes')
        ->assertJsonPath('revision', 2);

    $this->withToken('registry-secret')
        ->postJson('/servers', [
            'type' => 'reverb',
            'name' => 'unsupported-runtime',
            'hostname' => 'unsupported.example.test',
            'driver' => 'nomad',
        ])
        ->assertUnprocessable()
        ->assertJsonValidationErrors('driver');
});

it('manages logical Reverb servers through desired state', function (): void {
    $server = createReverbServer([
        'scaling' => [
            'enabled' => true,
            'server' => [
                'host' => 'redis.internal',
                'port' => 6380,
                'password' => 'private-redis-password',
                'database' => 4,
            ],
        ],
        'observability' => [
            'pulse_ingest_interval' => 20,
            'telescope_ingest_interval' => 25,
        ],
    ]);

    expect($server)
        ->desired_state->toBe('stopped')
        ->driver->toBe('local')
        ->replicas->toBe(2)
        ->revision->toBe(1)
        ->and($server['configuration']['scaling']['channel'])->toBe('gateway:reverb:'.$server['id']);
    expect($server['configuration']['scaling']['server'])
        ->not->toHaveKey('password')
        ->password_configured->toBeTrue()
        ->host->toBe('redis.internal')
        ->and($server['configuration']['observability']['pulse_ingest_interval'])->toBe(20);

    $storedScaling = DB::table(ServerTables::servers())->value('configuration');
    expect($storedScaling)->not->toContain('private-redis-password');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/provisioning/servers')
        ->assertOk()
        ->assertJsonPath('data.0.type', 'reverb')
        ->assertJsonPath('data.0.configuration.scaling_server.password', 'private-redis-password')
        ->assertJsonPath('data.0.configuration.pulse_ingest_interval', 20);

    $this->withToken('registry-secret')
        ->postJson("/servers/{$server['id']}/start")
        ->assertAccepted()
        ->assertJsonPath('desired_state', 'running')
        ->assertJsonPath('revision', 1)
        ->assertJsonPath('health.status', 'pending');

    $this->withToken('registry-secret')
        ->postJson("/servers/{$server['id']}/scale", ['replicas' => 3])
        ->assertAccepted()
        ->assertJsonPath('replicas', 3)
        ->assertJsonPath('revision', 2);

    $this->withToken('registry-secret')
        ->getJson("/servers/{$server['id']}/health")
        ->assertOk()
        ->assertJsonPath('status', 'pending')
        ->assertJsonPath('desired_instances', 3);

    $this->withToken('registry-secret')
        ->postJson("/servers/{$server['id']}/restart")
        ->assertAccepted()
        ->assertJsonPath('desired_state', 'running')
        ->assertJsonPath('revision', 3);

    $this->withToken('registry-secret')
        ->deleteJson("/servers/{$server['id']}")
        ->assertConflict();

    $this->withToken('registry-secret')
        ->postJson("/servers/{$server['id']}/stop")
        ->assertAccepted()
        ->assertJsonPath('desired_state', 'stopped');

    $this->withToken('registry-secret')
        ->deleteJson("/servers/{$server['id']}")
        ->assertNoContent();
});

it('stores Reverb credentials encrypted and omits secrets from resources', function (): void {
    $server = createReverbServer();
    $application = createReverbApplication($server['id']);

    expect($application)
        ->not->toHaveKey('secret')
        ->app_id->toBe('crm-production')
        ->key->toBe('public-app-key')
        ->revision->toBe(1);

    $rawSecret = DB::table(ServerTables::applications())->value('credentials');
    expect($rawSecret)->not->toBe('a-private-application-secret')
        ->and(ReverbApplication::query()->sole()->secret)->toBe('a-private-application-secret');

    $this->withToken('registry-secret')
        ->patchJson("/applications/{$application['id']}", [
            'secret' => 'the-rotated-application-secret',
            'max_connections' => 900,
        ])
        ->assertOk()
        ->assertJsonMissingPath('secret')
        ->assertJsonPath('max_connections', 900)
        ->assertJsonPath('revision', 2);

    expect(ReverbApplication::query()->sole()->secret)->toBe('the-rotated-application-secret');
});

it('provides only the applications assigned to the running server identity', function (): void {
    $first = createReverbServer();
    $second = createReverbServer([
        'name' => 'secondary-reverb',
        'hostname' => 'secondary-socket.example.test',
        'path' => '',
    ]);
    createReverbApplication($first['id']);
    createReverbApplication($second['id'], [
        'app_id' => 'secondary-app',
        'secret' => 'secondary-private-secret',
    ]);

    config()->set('gateway-reverb.server_id', $first['id']);
    $provider = app(ApplicationManager::class)->driver('gateway');

    expect($provider->all())->toHaveCount(1)
        ->and($provider->findById('crm-production')->key())->toBe('public-app-key')
        ->and($provider->findById('crm-production')->secret())->toBe('a-private-application-secret');

    expect(fn () => $provider->findById('secondary-app'))->toThrow(InvalidApplication::class);
});

it('exposes desired specifications and accepts observed instance reports only internally', function (): void {
    $server = createReverbServer(['driver' => 'docker']);

    $this->getJson('/_internal/provisioning/servers')->assertNotFound();

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/provisioning/servers')
        ->assertOk()
        ->assertJsonPath('data.0.id', $server['id'])
        ->assertJsonPath('data.0.desired_state', 'stopped')
        ->assertJsonPath('data.0.configuration.scaling_enabled', true);

    $instanceId = '019d4000-0000-7000-8000-000000000001';
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->putJson("/_internal/provisioning/servers/{$server['id']}/instances/{$instanceId}", [
            'node_id' => 'gateway-node-a',
            'runtime' => 'docker',
            'pid' => 4242,
            'host' => '127.0.0.1',
            'port' => 19001,
            'state' => 'running',
            'revision' => 1,
        ])
        ->assertOk()
        ->assertJsonPath('id', $instanceId)
        ->assertJsonPath('state', 'running');

    $this->withToken('registry-secret')
        ->getJson("/instances?server_id={$server['id']}")
        ->assertOk()
        ->assertJsonPath('data.0.node_id', 'gateway-node-a')
        ->assertJsonPath('data.0.port', 19001)
        ->assertJsonPath('data.0.runtime', 'docker');

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/provisioning/instances/reset', ['node_id' => 'gateway-node-a'])
        ->assertOk()
        ->assertJsonPath('updated', 1);

    $this->withToken('registry-secret')
        ->getJson("/instances?server_id={$server['id']}")
        ->assertOk()
        ->assertJsonPath('data.0.state', 'stopped')
        ->assertJsonPath('data.0.error', 'Gateway reconciler restarted.');

    $failedInstance = '019d4000-0000-7000-8000-000000000002';
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->putJson("/_internal/provisioning/servers/{$server['id']}/instances/{$failedInstance}", [
            'node_id' => 'gateway-node-a',
            'runtime' => 'kubernetes',
            'state' => 'failed',
            'revision' => 1,
            'error' => 'Pod admission denied.',
        ])
        ->assertOk()
        ->assertJsonPath('runtime', 'kubernetes')
        ->assertJsonPath('host', null)
        ->assertJsonPath('port', null);
});

it('fails closed when no managed Reverb server identity is configured', function (): void {
    createReverbServer();
    config()->set('gateway-reverb.server_id', null);

    expect(fn () => app(ApplicationManager::class)->driver('gateway')->all())
        ->toThrow(RuntimeException::class, 'GATEWAY_REVERB_SERVER_ID');
});

it('protects the Reverb management API with the registry token', function (): void {
    $this->postJson('/servers', [])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/reverb/servers', [])->assertNotFound();
});
