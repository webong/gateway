<?php

declare(strict_types=1);

use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Models\ProvisioningNode;
use Webong\Gateway\Servers\Models\Instance;

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
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/019d4000-0000-7000-8000-000000000001", [
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
        ->putJson('/_internal/provisioning/nodes/node-a/assignments', ['drivers' => ['docker']])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');
    $second = $this->withHeaders($headers)
        ->putJson('/_internal/provisioning/nodes/node-b/assignments', ['drivers' => ['docker']])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');

    $first = $this->withHeaders($headers)
        ->putJson('/_internal/provisioning/nodes/node-a/assignments', ['drivers' => ['docker']])
        ->assertOk()
        ->assertJsonPath('meta.leader_id', 'node-a');
    $firstIds = collect($first->json('data'))->pluck('assignment_id');
    $secondIds = collect($second->json('data'))->pluck('assignment_id');

    expect($firstIds)->toHaveCount(2)
        ->and($secondIds)->toHaveCount(1)
        ->and($firstIds->intersect($secondIds))->toHaveCount(0)
        ->and($second->json('data.0.id'))->toBe($server->getKey());
});

it('fences concurrent provisioning worker sessions and permits graceful handover', function (): void {
    $server = Server::query()->create([
        'type' => 'reverb', 'name' => 'fenced-worker', 'hostname' => 'fenced.example.test',
        'path' => '', 'driver' => 'docker', 'desired_state' => 'running',
        'replicas' => 1, 'revision' => 1, 'configuration' => [],
    ]);
    $firstSession = str_repeat('a', 32);
    $secondSession = str_repeat('b', 32);
    $internal = ['X-Gateway-Internal' => 'internal-secret'];
    $first = $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'session_id' => $firstSession,
    ], $internal)->assertOk()->assertJsonPath('meta.session_id', $firstSession);
    $instanceId = $first->json('data.0.assignment_id');
    expect($instanceId)->toBeString();
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'session_id' => $secondSession,
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
    expect(Instance::query()->findOrFail($instanceId)->error)->toBe('Gateway reconciler restarted.');
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertOk()->assertJsonPath('error', null);

    $this->deleteJson('/_internal/provisioning/nodes/node-fenced/session', ['session_id' => $secondSession], $internal)->assertConflict();
    $this->deleteJson('/_internal/provisioning/nodes/node-fenced/session', ['session_id' => $firstSession], $internal)->assertNoContent();
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertConflict();
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'session_id' => $secondSession,
    ], $internal)->assertOk();
    $this->putJson("/_internal/provisioning/servers/{$server->getKey()}/instances/{$instanceId}", $report,
        [...$internal, 'X-Gateway-Worker-Session' => $firstSession])->assertConflict();

    ProvisioningNode::query()->whereKey('node-fenced')->update(['heartbeat_at' => now()->subSeconds(30)]);
    $this->putJson('/_internal/provisioning/nodes/node-fenced/assignments', [
        'drivers' => ['docker'], 'session_id' => $firstSession,
    ], $internal)->assertOk();
});
