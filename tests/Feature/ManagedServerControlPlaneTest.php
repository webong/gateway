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

it('registers targets with one-time scoped worker credentials and immutable connection references', function (): void {
    $this->postJson('/targets', [])->assertUnauthorized();
    $target = $this->withToken('registry-secret')->postJson('/targets', [
        'name' => 'production', 'driver' => 'docker',
        'connection' => ['context' => 'production-ssh', 'publish_host' => '10.0.0.10', 'route_host' => '10.0.0.10'],
    ])->assertCreated()->json();
    expect($target['worker_token'])->toHaveLength(64);
    $stored = \Webong\Gateway\Servers\Models\DeploymentTarget::query()->findOrFail($target['id']);
    expect($stored->worker_token_hash)->toBe(hash('sha256', $target['worker_token']))
        ->and($stored->getRawOriginal('connection'))->not->toContain('production-ssh');
    $this->withToken('registry-secret')->getJson('/targets')->assertOk()
        ->assertJsonMissingPath('data.0.worker_token')->assertJsonMissingPath('data.0.worker_token_hash');
    $this->withToken('registry-secret')->patchJson("/targets/{$target['id']}", ['connection' => []])->assertStatus(405);
    $headers = ['X-Gateway-Target' => $target['id'], 'X-Gateway-Internal' => $target['worker_token']];
    $this->getJson("/_internal/provisioning/targets/{$target['id']}", $headers)->assertOk();
    $this->getJson('/_internal/provisioning/servers', $headers)->assertNotFound();
    $this->withToken('registry-secret')->postJson("/targets/{$target['id']}/rotate-token")->assertOk();
    $this->getJson("/_internal/provisioning/targets/{$target['id']}", $headers)->assertNotFound();
});

it('rejects raw secrets and incomplete or unknown target connection settings', function (): void {
    foreach ([
        ['driver' => 'docker', 'connection' => ['context' => 'prod']],
        ['driver' => 'local', 'connection' => ['private_key' => 'secret']],
        ['driver' => 'kubernetes', 'connection' => ['context' => 'prod', 'kubeconfig' => 'relative', 'namespace' => 'apps', 'route_mode' => 'pod-ip']],
        ['driver' => 'kubernetes', 'connection' => ['context' => 'prod', 'kubeconfig' => '/run/kubeconfig', 'namespace' => 'apps', 'route_mode' => 'pod-ip', 'token' => 'secret']],
    ] as $attributes) {
        $this->withToken('registry-secret')->postJson('/targets', ['name' => 'invalid', ...$attributes])->assertUnprocessable();
    }
});

it('isolates assignments and reports by deployment target while preserving legacy placement', function (): void {
    $targets = [];
    foreach (['east', 'west'] as $name) {
        $target = $this->withToken('registry-secret')->postJson('/targets', ['name' => $name, 'driver' => 'local'])->assertCreated()->json();
        $server = $this->withToken('registry-secret')->postJson('/servers', [
            'type' => 'test', 'name' => $name, 'hostname' => "$name.example.test", 'target_id' => $target['id'],
        ])->assertCreated()->assertJsonPath('target_id', $target['id'])->json();
        $this->withToken('registry-secret')->postJson("/servers/{$server['id']}/start")->assertAccepted();
        $targets[$name] = [$target, $server];
    }
    [$east, $eastServer] = $targets['east'];
    [$west, $westServer] = $targets['west'];
    $eastHeaders = ['X-Gateway-Target' => $east['id'], 'X-Gateway-Internal' => $east['worker_token']];
    $westHeaders = ['X-Gateway-Target' => $west['id'], 'X-Gateway-Internal' => $west['worker_token']];
    $assignment = $this->putJson('/_internal/provisioning/nodes/east-worker/assignments', ['drivers' => ['local']], $eastHeaders)
        ->assertOk()->assertJsonCount(1, 'data')->assertJsonPath('data.0.id', $eastServer['id'])->json('data.0');
    $this->putJson('/_internal/provisioning/nodes/west-worker/assignments', ['drivers' => ['local']], $westHeaders)
        ->assertOk()->assertJsonCount(1, 'data')->assertJsonPath('data.0.id', $westServer['id']);
    $this->putJson('/_internal/provisioning/nodes/legacy/assignments', ['drivers' => ['local']], ['X-Gateway-Internal' => 'internal-secret'])
        ->assertOk()->assertJsonCount(0, 'data');
    $this->putJson('/_internal/provisioning/nodes/east-worker/assignments', ['drivers' => ['local']], $westHeaders)->assertConflict();
    $this->putJson('/_internal/provisioning/nodes/east-worker/assignments', ['drivers' => ['docker']], $eastHeaders)->assertUnprocessable();
    $this->getJson("/_internal/provisioning/targets/{$west['id']}", $eastHeaders)->assertNotFound();
    $report = ['node_id' => 'east-worker', 'runtime' => 'local', 'host' => '10.0.0.1', 'port' => 8080, 'state' => 'running', 'revision' => 1];
    $this->putJson("/_internal/provisioning/servers/{$westServer['id']}/instances/{$assignment['assignment_id']}", $report, $eastHeaders)->assertNotFound();
    $this->putJson("/_internal/provisioning/servers/{$eastServer['id']}/instances/{$assignment['assignment_id']}", $report, $eastHeaders)->assertOk();
    $this->postJson('/_internal/provisioning/instances/reset', ['node_id' => 'east-worker'], $westHeaders)->assertNotFound();
    $this->withToken('registry-secret')->deleteJson("/targets/{$east['id']}")->assertConflict();
});

it('infers the target driver and disallows implicit relocation', function (): void {
    $target = $this->withToken('registry-secret')->postJson('/targets', [
        'name' => 'cluster', 'driver' => 'kubernetes',
        'connection' => ['context' => 'prod', 'kubeconfig' => '/run/secrets/kubeconfig', 'namespace' => 'apps', 'route_mode' => 'pod-ip'],
    ])->assertCreated()->json();
    $input = ['type' => 'test', 'name' => 'events', 'hostname' => 'events.example.test', 'target_id' => $target['id']];
    $this->withToken('registry-secret')->postJson('/servers', [...$input, 'driver' => 'local'])->assertUnprocessable();
    $server = $this->withToken('registry-secret')->postJson('/servers', $input)->assertCreated()->assertJsonPath('driver', 'kubernetes')->json();
    $this->withToken('registry-secret')->patchJson("/servers/{$server['id']}", ['driver' => 'local'])->assertUnprocessable();
    $this->withToken('registry-secret')->patchJson("/servers/{$server['id']}", ['target_id' => null])->assertUnprocessable();
    $this->withToken('registry-secret')->deleteJson("/servers/{$server['id']}")->assertNoContent();
    $this->withToken('registry-secret')->deleteJson("/targets/{$target['id']}")->assertNoContent();
});
