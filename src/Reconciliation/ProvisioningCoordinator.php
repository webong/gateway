<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation;

use Carbon\CarbonImmutable;
use Webong\Gateway\Servers\Models\ProvisioningNode;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Resources\ServerResources;

/**
 * Keeps placement in the shared control plane. A slot is deterministic, so a
 * node restart resumes its own instance and a departed node's slots move only
 * after its lease expires.
 */
final class ProvisioningCoordinator
{
    /** @param list<string> $drivers
     *  @return array{data: list<array<string, mixed>>, leader_id: string|null}
     */
    public function assignments(string $nodeId, array $drivers): array
    {
        $now = CarbonImmutable::now();
        $drivers = array_values(array_unique(array_filter($drivers, static fn (mixed $driver): bool => is_string($driver) && in_array($driver, ['local', 'docker', 'kubernetes'], true))));
        sort($drivers);

        ProvisioningNode::query()->updateOrCreate(['id' => $nodeId], [
            'drivers' => $drivers,
            'heartbeat_at' => $now,
        ]);

        $lease = max(2, (int) config('gateway.servers.coordination_lease_seconds', 15));
        $nodes = ProvisioningNode::query()
            ->where('heartbeat_at', '>=', $now->subSeconds($lease))
            ->orderBy('id')
            ->get();
        $leader = $nodes->first()?->getKey();
        $assigned = [];

        foreach (Server::query()->where('desired_state', 'running')->orderBy('id')->get() as $server) {
            $eligible = $nodes
                ->filter(static fn (ProvisioningNode $node): bool => in_array($server->driver, $node->drivers ?? [], true))
                ->values();
            if ($eligible->isEmpty()) {
                continue;
            }

            for ($slot = 0; $slot < $server->replicas; $slot++) {
                $node = $eligible->get($slot % $eligible->count());
                if ($node->getKey() !== $nodeId) {
                    continue;
                }
                $spec = ServerResources::specification($server);
                $spec['assignment_id'] = self::slotId((string) $server->getKey(), (int) $server->revision, $slot);
                $assigned[] = $spec;
            }
        }

        return ['data' => $assigned, 'leader_id' => $leader];
    }

    private static function slotId(string $serverId, int $revision, int $slot): string
    {
        $hex = substr(hash('sha256', "gateway-placement\0{$serverId}\0{$revision}\0{$slot}"), 0, 32);
        $hex[12] = '5';
        $hex[16] = dechex((hexdec($hex[16]) & 0x3) | 0x8);

        return substr($hex, 0, 8).'-'.substr($hex, 8, 4).'-'.substr($hex, 12, 4).'-'.substr($hex, 16, 4).'-'.substr($hex, 20);
    }
}
