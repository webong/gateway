<?php

declare(strict_types=1);

use Webong\Gateway\Laravel\Servers\Models\Instance;
use Webong\Gateway\Laravel\Servers\Models\ProvisioningNode;
use Webong\Gateway\Laravel\Servers\Models\Server;

it('exposes workload-neutral specifications to the reconciler', function (): void {
    Server::query()->create([
        'type' => 'centrifugo',
        'name' => 'events',
        'hostname' => 'events.example.test',
        'path' => '/connection',
        'driver' => 'docker',
        'desired_state' => 'stopped',
        'replicas' => 1,
        'revision' => 1,
        'configuration' => ['client_insecure' => false],
    ]);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/provisioning/servers')
        ->assertOk()
        ->assertJsonPath('data.0.type', 'centrifugo')
        ->assertJsonPath('data.0.configuration.client_insecure', false);
});

it('accepts observed instance reports only through the internal API', function (): void {
    $server = Server::query()->create([
        'type' => 'reverb',
        'name' => 'socket',
        'hostname' => 'socket.example.test',
        'path' => '',
        'driver' => 'kubernetes',
        'desired_state' => 'running',
        'replicas' => 2,
        'revision' => 1,
        'configuration' => [],
    ]);

    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/instance-a", [])->assertNotFound();
    $claim = $this->putJson('/_internal/provisioning/nodes/node-a/assignments', [
        'drivers' => ['kubernetes'], 'execution_protocol' => 'workload/v1',
        'owner_id' => str_repeat('c', 32), 'session_id' => str_repeat('a', 32),
    ], ['X-Gateway-Internal' => 'internal-secret'])->assertOk();
    $instanceId = $claim->json('data.0.assignment_id');
    $this->withHeaders(['X-Gateway-Internal' => 'internal-secret',
        'X-Gateway-Worker-Owner' => str_repeat('c', 32), 'X-Gateway-Worker-Session' => str_repeat('a', 32)])
        ->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", [
            'node_id' => 'node-a',
            'runtime' => 'kubernetes',
            'host' => '10.0.0.2',
            'port' => 8080,
            'state' => 'running',
            'revision' => 1,
        ])->assertOk()->assertJsonPath('runtime', 'kubernetes');
});

it('coordinates deterministic replica placements across live compatible nodes', function (): void {
    $server = Server::query()->create([
        'type' => 'reverb',
        'name' => 'socket-cluster',
        'hostname' => 'cluster.example.test',
        'path' => '',
        'driver' => 'docker',
        'desired_state' => 'running',
        'replicas' => 3,
        'revision' => 2,
        'configuration' => [],
    ]);

    $headers = ['X-Gateway-Internal' => 'internal-secret'];
    $first = $this->withHeaders($headers)
        ->putJson('/_internal/provisioning/nodes/node-a/assignments', ['drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => str_repeat('a', 32)])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');
    $second = $this->withHeaders($headers)
        ->putJson('/_internal/provisioning/nodes/node-b/assignments', ['drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => str_repeat('a', 32)])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');

    $first = $this->withHeaders($headers)
        ->putJson('/_internal/provisioning/nodes/node-a/assignments', ['drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => str_repeat('a', 32)])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');
    $firstIds = collect($first->json('data'))->pluck('assignment_id');
    $secondIds = collect($second->json('data'))->pluck('assignment_id');

    expect($firstIds)->toHaveCount(3)
        ->and($secondIds)->toHaveCount(0)
        ->and($firstIds->intersect($secondIds))->toHaveCount(0)
        ->and($first->json('data.0.id'))->toBe($server->getKey());
});

it('fences concurrent provisioning worker sessions and permits graceful handover', function (): void {
    $server = Server::query()->create([
        'type' => 'reverb', 'name' => 'fenced-worker', 'hostname' => 'fenced.example.test',
        'path' => '', 'driver' => 'docker', 'desired_state' => 'running',
        'replicas' => 1, 'revision' => 1, 'configuration' => [],
    ]);
    $firstSession = str_repeat('a', 32);
    $secondSession = str_repeat('b', 32);
    $internal = ['X-Gateway-Internal' => 'internal-secret', 'X-Gateway-Worker-Owner' => str_repeat('c', 32)];
    $first = $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => $firstSession,
    ], $internal)->assertOk()->assertJsonPath('meta.session_id', $firstSession);
    $instanceId = $first->json('data.0.assignment_id');
    expect($instanceId)->toBeString();
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => $secondSession,
    ], $internal)->assertStatus(423);

    $report = [
        'node_id' => 'node-fenced', 'runtime' => 'docker', 'host' => '10.0.0.4',
        'port' => 8080, 'state' => 'running', 'revision' => 1,
    ];
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $secondSession])->assertConflict();
    $this->postJson('/_internal/provisioning/instances/reset', ['node_id' => 'node-fenced'],
        [...$internal, 'X-Gateway-Worker-Session' => $secondSession])->assertConflict();
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertOk();
    $this->postJson('/_internal/provisioning/instances/reset', ['node_id' => 'node-fenced'],
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertOk();
    expect(Instance::query()->findOrFail($instanceId)->error)->toBe('Gateway observation reset; termination remains unconfirmed.');
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertOk()->assertJsonPath('error', null);

    $this->deleteJson('/_internal/provisioning/nodes/node-fenced/session', ['session_id' => $secondSession, 'cleanup_confirmed' => true], $internal)->assertConflict();
    $this->deleteJson('/_internal/provisioning/nodes/node-fenced/session', ['session_id' => $firstSession, 'cleanup_confirmed' => true], $internal)->assertNoContent();
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertConflict();
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => $secondSession,
    ], $internal)->assertOk();
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertConflict();

    ProvisioningNode::query()->whereKey('node-fenced')->update(['heartbeat_at' => now()->subSeconds(30)]);
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'execution_protocol' => 'workload/v1', 'owner_id' => str_repeat('c', 32), 'session_id' => $firstSession,
    ], $internal)->assertOk();
});

it('retains reservations across revision changes and expired node heartbeats', function (): void {
    $server = Server::query()->create([
        'type' => 'reverb', 'name' => 'exclusive', 'hostname' => 'exclusive.example.test',
        'path' => '', 'driver' => 'docker', 'desired_state' => 'running',
        'replicas' => 1, 'revision' => 1, 'configuration' => [],
    ]);
    $claim = ['drivers' => ['docker'], 'execution_protocol' => 'workload/v1',
        'owner_id' => str_repeat('a', 32), 'session_id' => str_repeat('b', 32)];
    $headers = ['X-Gateway-Internal' => 'internal-secret'];
    $first = $this->putJson('/_internal/provisioning/nodes/node-a/assignments', $claim, $headers)
        ->assertOk()->assertJsonPath('meta.execution_protocol', 'workload/v1');
    $id = $first->json('data.0.assignment_id');
    expect(Instance::query()->findOrFail($id)->state)->toBe('starting');
    $server->update(['revision' => 2]);
    $this->putJson('/_internal/provisioning/nodes/node-a/assignments', $claim, $headers)
        ->assertOk()->assertJsonPath('data.0.assignment_id', $id)->assertJsonPath('data.0.revision', 2);
    ProvisioningNode::query()->whereKey('node-a')->update(['heartbeat_at' => now()->subMinute()]);
    $this->putJson('/_internal/provisioning/nodes/node-b/assignments', $claim, $headers)
        ->assertOk()->assertJsonCount(0, 'data');
    // A different journal cannot take over the old node even after lease expiry.
    $this->putJson('/_internal/provisioning/nodes/node-a/assignments', [...$claim, 'owner_id' => str_repeat('c', 32)], $headers)
        ->assertStatus(423);
    expect(Instance::query()->findOrFail($id)->node_id)->toBe('node-a');
});

it('requires a versioned worker and an explicit legacy drain before adoption', function (): void {
    $headers = ['X-Gateway-Internal' => 'internal-secret'];
    $this->putJson('/_internal/provisioning/nodes/legacy/assignments', ['drivers' => ['docker']], $headers)
        ->assertUnprocessable();
    ProvisioningNode::query()->create(['id' => 'legacy', 'drivers' => ['docker'], 'heartbeat_at' => now()->subMinute()]);
    $claim = ['drivers' => ['docker'], 'execution_protocol' => 'workload/v1',
        'owner_id' => str_repeat('a', 32), 'session_id' => str_repeat('b', 32)];
    $this->putJson('/_internal/provisioning/nodes/legacy/assignments', $claim, $headers)->assertStatus(423);
    $this->putJson('/_internal/provisioning/nodes/legacy/assignments', [...$claim, 'legacy_drained' => true], $headers)->assertOk();
    $this->deleteJson('/_internal/provisioning/nodes/legacy/session', ['session_id' => str_repeat('b', 32)],
        [...$headers, 'X-Gateway-Worker-Owner' => str_repeat('a', 32)])->assertUnprocessable();
});
