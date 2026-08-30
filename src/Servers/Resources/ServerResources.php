<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Resources;

use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Instance;
use Webong\Gateway\Servers\Models\Server;

final class ServerResources
{
    /** @return array<string, mixed> */
    public static function server(Server $server, ServerType $type): array
    {
        return [
            'id' => $server->getKey(),
            'type' => $server->type,
            'name' => $server->name,
            'hostname' => $server->hostname,
            'path' => $server->path,
            'driver' => $server->driver,
            'desired_state' => $server->desired_state,
            'replicas' => $server->replicas,
            'revision' => $server->revision,
            'configuration' => $type->configurationResource($server),
            'capabilities' => ['applications' => $type->supportsApplications()],
            'health' => self::health($server),
            'applications_count' => $server->applications_count ?? $server->applications()->count(),
            'instances_count' => $server->instances_count ?? $server->instances()->whereIn('state', ['starting', 'running'])->count(),
            'created_at' => $server->created_at?->toAtomString(),
            'updated_at' => $server->updated_at?->toAtomString(),
        ];
    }

    /** @return array<string, mixed> */
    public static function health(Server $server): array
    {
        $instances = $server->instances()->get(['state', 'heartbeat_at']);
        $states = $instances->countBy('state');
        $starting = (int) ($states['starting'] ?? 0);
        $running = (int) ($states['running'] ?? 0);
        $stopping = (int) ($states['stopping'] ?? 0);
        $failed = (int) ($states['failed'] ?? 0);
        $active = $starting + $running + $stopping;

        if ($server->desired_state === 'stopped') {
            $status = $active === 0 ? 'stopped' : 'stopping';
        } elseif ($running >= $server->replicas) {
            $status = 'healthy';
        } elseif ($running > 0) {
            $status = 'degraded';
        } elseif ($starting > 0) {
            $status = 'starting';
        } elseif ($failed > 0) {
            $status = 'unhealthy';
        } else {
            $status = 'pending';
        }

        return [
            'status' => $status,
            'desired_instances' => $server->desired_state === 'running' ? $server->replicas : 0,
            'starting_instances' => $starting,
            'running_instances' => $running,
            'stopping_instances' => $stopping,
            'failed_instances' => $failed,
            'last_heartbeat_at' => $instances->max('heartbeat_at')?->toAtomString(),
        ];
    }

    /** @return array<string, mixed> */
    public static function specification(Server $server): array
    {
        return [
            'id' => $server->getKey(),
            'type' => $server->type,
            'name' => $server->name,
            'hostname' => $server->hostname,
            'path' => $server->path,
            'driver' => $server->driver,
            'desired_state' => $server->desired_state,
            'replicas' => $server->replicas,
            'revision' => $server->revision,
            'configuration' => $server->configuration ?? (object) [],
        ];
    }

    /** @return array<string, mixed> */
    public static function instance(Instance $instance): array
    {
        return [
            'id' => $instance->getKey(),
            'server_id' => $instance->server_id,
            'node_id' => $instance->node_id,
            'runtime' => $instance->runtime,
            'pid' => $instance->pid,
            'host' => $instance->host,
            'port' => $instance->port,
            'state' => $instance->state,
            'revision' => $instance->revision,
            'error' => $instance->error,
            'started_at' => $instance->started_at?->toAtomString(),
            'heartbeat_at' => $instance->heartbeat_at?->toAtomString(),
            'stopped_at' => $instance->stopped_at?->toAtomString(),
        ];
    }
}
