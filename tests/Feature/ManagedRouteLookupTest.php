<?php

declare(strict_types=1);

use Webong\Gateway\Servers\Models\Instance;
use Webong\Gateway\Servers\Models\ProvisioningNode;
use Webong\Gateway\Servers\Models\Server;

it('resolves only healthy current managed instances through the shared internal credential', function (): void {
    $root = Server::query()->create([
        'type' => 'centrifugo', 'name' => 'route-root', 'hostname' => 'events.example.test',
        'path' => '', 'driver' => 'kubernetes', 'desired_state' => 'running',
        'replicas' => 1, 'revision' => 1, 'configuration' => [],
    ]);
    $specific = Server::query()->create([
        'type' => 'reverb', 'name' => 'route-specific', 'hostname' => 'events.example.test',
        'path' => '/socket', 'driver' => 'kubernetes', 'desired_state' => 'running',
        'replicas' => 1, 'revision' => 2, 'configuration' => [],
    ]);
    ProvisioningNode::query()->create([
        'id' => 'node-route', 'target_id' => null, 'session_id' => 'legacy',
        'drivers' => ['kubernetes'], 'heartbeat_at' => now(),
    ]);
    foreach ([[$root, '10.0.0.2'], [$specific, '10.0.0.3']] as [$server, $host]) {
        Instance::query()->create([
            'id' => (string) \Illuminate\Support\Str::uuid(), 'server_id' => $server->getKey(),
            'node_id' => 'node-route', 'runtime' => 'kubernetes', 'host' => $host,
            'port' => 8000, 'state' => 'running', 'revision' => $server->revision,
            'heartbeat_at' => now(),
        ]);
    }

    $route = '/_internal/provisioning/routes/resolve?hostname=EVENTS.EXAMPLE.TEST&path=%2Fsocket%2Fapp';
    $this->getJson($route)->assertNotFound();
    $this->getJson($route, ['X-Gateway-Target' => 'other', 'X-Gateway-Internal' => 'internal-secret'])->assertNotFound();
    $this->getJson($route, ['X-Gateway-Internal' => 'internal-secret'])
        ->assertOk()->assertJsonPath('data.server_id', $specific->getKey())->assertJsonPath('data.host', '10.0.0.3');
    $this->getJson('/_internal/provisioning/routes/resolve?hostname=events.example.test&path=%2Fsocketed', ['X-Gateway-Internal' => 'internal-secret'])
        ->assertOk()->assertJsonPath('data.server_id', $root->getKey());
    $this->getJson('/_internal/provisioning/routes/resolve?hostname=unmanaged.example.test&path=%2F', ['X-Gateway-Internal' => 'internal-secret'])
        ->assertNoContent();

    $specific->update(['desired_state' => 'stopped']);
    $this->getJson($route, ['X-Gateway-Internal' => 'internal-secret'])->assertStatus(503);
    $specific->update(['desired_state' => 'running']);

    $specific->update(['revision' => 3]);
    $this->getJson($route, ['X-Gateway-Internal' => 'internal-secret'])->assertServiceUnavailable();
    $specific->update(['revision' => 2]);
    ProvisioningNode::query()->whereKey('node-route')->update(['heartbeat_at' => now()->subMinute()]);
    $this->getJson($route, ['X-Gateway-Internal' => 'internal-secret'])->assertServiceUnavailable();
    ProvisioningNode::query()->whereKey('node-route')->update(['heartbeat_at' => now()]);
    Instance::query()->where('server_id', $specific->getKey())->update(['heartbeat_at' => now()->subMinute()]);
    $this->getJson($route, ['X-Gateway-Internal' => 'internal-secret'])->assertServiceUnavailable();
});
