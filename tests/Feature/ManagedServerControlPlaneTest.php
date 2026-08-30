<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerTables;
use Webong\Gateway\Servers\ServerTypeRegistry;

beforeEach(function (): void {
    app(ServerTypeRegistry::class)->register(new class implements ServerType
    {
        public function type(): string
        {
            return 'test';
        }

        public function defaultPath(): string
        {
            return '/socket';
        }

        public function validateConfiguration(array $configuration, ?Server $server, int $replicas): array
        {
            return $configuration;
        }

        public function configurationForStorage(array $validated, array $current, string $serverId): array
        {
            return [...$current, ...$validated];
        }

        public function configurationResource(Server $server): array
        {
            return ['configured' => ($server->configuration['secret'] ?? '') !== ''];
        }

        public function supportsApplications(): bool
        {
            return false;
        }
    });
});

it('owns configurable shared schemas and the unified desired-state API', function (): void {
    expect(ServerTables::servers())->toBe('managed_servers')
        ->and(ServerTables::applications())->toBe('managed_applications')
        ->and(ServerTables::instances())->toBe('managed_instances')
        ->and(Schema::hasTable('managed_servers'))->toBeTrue()
        ->and(Schema::hasTable('managed_applications'))->toBeTrue()
        ->and(Schema::hasTable('managed_instances'))->toBeTrue();

    $server = $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'test',
        'name' => 'notifications',
        'hostname' => 'socket.example.test',
        'driver' => 'kubernetes',
        'replicas' => 3,
        'configuration' => ['secret' => 'private'],
    ])->assertCreated()
        ->assertJsonPath('type', 'test')
        ->assertJsonPath('path', '/socket')
        ->assertJsonPath('configuration.configured', true)
        ->assertJsonMissing(['secret' => 'private'])
        ->json();

    $this->withToken('registry-secret')->postJson("/servers/{$server['id']}/start")
        ->assertAccepted()->assertJsonPath('desired_state', 'running')->assertJsonPath('revision', 1);
    $this->withToken('registry-secret')->postJson("/servers/{$server['id']}/restart")
        ->assertAccepted()->assertJsonPath('revision', 2);
    $this->withToken('registry-secret')->postJson('/applications', ['server_id' => $server['id']])
        ->assertUnprocessable()->assertJsonPath('message', 'Server type [test] does not support applications.');
});

it('rejects unavailable types and protects the unified API', function (): void {
    $this->postJson('/servers', [])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/servers', [
        'type' => 'missing',
        'name' => 'missing',
        'hostname' => 'missing.example.test',
    ])->assertUnprocessable()->assertJsonValidationErrors('type');
});
