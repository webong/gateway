<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Reconciliation;

use Carbon\CarbonImmutable;
use Illuminate\Support\Facades\DB;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Laravel\Servers\Models\ProvisioningNode;
use Webong\Gateway\Laravel\Servers\Models\Instance;
use Webong\Gateway\Laravel\Servers\Models\Server;
use Webong\Gateway\Laravel\Servers\Resources\ServerResources;

/**
 * Keeps placement in the shared control plane. A slot is deterministic, so a
 * node restart resumes its own instance. Slots move only after confirmed
 * termination; permission or heartbeat expiry alone cannot prove that.
 */
final class ProvisioningCoordinator
{
    /** @param list<string> $drivers
     *  @return array{data: list<array<string, mixed>>, leader_id: string|null}
     */
    public function assignments(string $nodeId, array $drivers, ?string $targetId = null, string $sessionId = 'legacy', ?string $ownerId = null, bool $legacyDrained = false): array
    {
        $now = CarbonImmutable::now();
        $drivers = array_values(array_unique(array_filter($drivers, static fn (mixed $driver): bool => is_string($driver) && in_array($driver, ['local', 'docker', 'kubernetes'], true))));
        sort($drivers);

        $lease = max(2, (int) config('gateway.servers.coordination_lease_seconds', 15));
        $this->claimNode($nodeId, $drivers, $targetId, $sessionId, $now, $lease, $ownerId, $legacyDrained);
        $nodes = ProvisioningNode::query()
            ->where('target_id', $targetId)
            ->where('heartbeat_at', '>=', $now->subSeconds($lease))
            ->orderBy('id')
            ->get();
        $leader = $nodes->first()?->getKey();
        $assigned = [];

        DB::transaction(function () use ($nodeId, $targetId, $nodes, &$assigned): void {
            // Serialize placement against the existing server record, not a
            // separate infrastructure ledger. A reservation precedes execution.
            foreach (Server::query()->where('target_id', $targetId)->where('desired_state', 'running')->orderBy('id')->lockForUpdate()->get() as $server) {
                $eligible = $nodes->filter(static fn (ProvisioningNode $node): bool => in_array($server->driver, $node->drivers ?? [], true))->values();
                if ($eligible->isEmpty()) {
                    continue;
                }
                $instances = Instance::query()->where('server_id', $server->getKey())->lockForUpdate()->get();
                $slots = [];
                for ($slot = 0; $slot < $server->replicas; $slot++) {
                    $slots[] = self::slotId((string) $server->getKey(), $slot);
                }
                // Old revision-derived identities require explicit drain, never
                // silent adoption or parallel old/new execution.
                if ($instances->contains(static fn (Instance $instance): bool => ! in_array($instance->getKey(), $slots, true) && $instance->state !== 'stopped')) {
                    continue;
                }
                foreach ($slots as $slot => $id) {
                    $existing = $instances->firstWhere('id', $id);
                    $node = $eligible->get($slot % $eligible->count());
                    if ($existing !== null && $existing->state !== 'stopped') {
                        // Heartbeat expiry is not termination/fencing evidence.
                        $node = $eligible->firstWhere('id', $existing->node_id);
                    }
                    if ($node?->getKey() !== $nodeId) {
                        continue;
                    }
                    if ($existing === null || $existing->state === 'stopped') {
                        Instance::query()->updateOrCreate(['id' => $id], [
                            'server_id' => $server->getKey(), 'node_id' => $nodeId,
                            'runtime' => $server->driver, 'state' => 'starting',
                            'revision' => $server->revision, 'host' => null, 'port' => null,
                            'pid' => null, 'endpoints' => null,
                        ]);
                    }
                    $spec = ServerResources::specification($server);
                    $spec['assignment_id'] = $id;
                    $assigned[] = $spec;
                }
            }
        });

        return ['data' => $assigned, 'leader_id' => $leader, 'issued_at_ms' => $now->getTimestampMs(), 'permission_expires_at_ms' => $now->addSeconds($lease)->getTimestampMs()];
    }

    /** @param list<string> $drivers */
    private function claimNode(string $nodeId, array $drivers, ?string $targetId, string $sessionId, CarbonImmutable $now, int $lease, ?string $ownerId, bool $legacyDrained): void
    {
        DB::transaction(static function () use ($nodeId, $drivers, $targetId, $sessionId, $now, $lease, $ownerId, $legacyDrained): void {
            ProvisioningNode::query()->insertOrIgnore([
                'id' => $nodeId,
                'target_id' => $targetId,
                'session_id' => $sessionId,
                'owner_id' => $ownerId,
                'drivers' => json_encode($drivers, JSON_THROW_ON_ERROR),
                'heartbeat_at' => $now,
                'created_at' => $now,
                'updated_at' => $now,
            ]);
            $existing = ProvisioningNode::query()->lockForUpdate()->findOrFail($nodeId);
            abort_if($existing->target_id !== $targetId, 409, 'Node IDs cannot be reused across deployment targets.');
            if ($existing->owner_id === null && $ownerId !== null) {
                abort_unless($legacyDrained && ! Instance::query()->where('node_id', $nodeId)->where('state', '!=', 'stopped')->exists(), Response::HTTP_LOCKED, 'Drain and verify the legacy worker before Workload adoption.');
            } else {
                abort_if($existing->owner_id !== $ownerId, Response::HTTP_LOCKED, 'This node belongs to another persistent worker owner.');
            }
            $activeSession = $existing->session_id ?? 'legacy';
            abort_if($activeSession !== $sessionId && $existing->heartbeat_at?->greaterThanOrEqualTo($now->subSeconds($lease)), Response::HTTP_LOCKED, 'Another worker session owns this node.');
            $existing->update(['owner_id' => $ownerId, 'session_id' => $sessionId, 'drivers' => $drivers, 'heartbeat_at' => $now]);
        });
    }

    public function release(string $nodeId, string $sessionId, ?string $targetId = null, ?string $ownerId = null): bool
    {
        return DB::transaction(static function () use ($nodeId, $sessionId, $targetId, $ownerId): bool {
            $node = ProvisioningNode::query()->whereKey($nodeId)->where('target_id', $targetId)
                ->where('session_id', $sessionId)->where('owner_id', $ownerId)->lockForUpdate()->first();
            if ($node === null) {
                return false;
            }
            // The versioned client calls release only after Workload confirms
            // cleanup, including interrupted creations discovered on shutdown.
            Instance::query()->where('node_id', $nodeId)->whereIn('server_id', Server::query()->where('target_id', $targetId)->select('id'))
                ->update(['state' => 'stopped', 'stopped_at' => CarbonImmutable::now()]);
            $node->update(['session_id' => null, 'heartbeat_at' => CarbonImmutable::now()->subYears(1)]);
            return true;
        });
    }

    private static function slotId(string $serverId, int $slot): string
    {
        $hex = substr(hash('sha256', "gateway-workload-placement\0{$serverId}\0{$slot}"), 0, 32);
        $hex[12] = '5';
        $hex[16] = dechex((hexdec($hex[16]) & 0x3) | 0x8);

        return substr($hex, 0, 8).'-'.substr($hex, 8, 4).'-'.substr($hex, 12, 4).'-'.substr($hex, 16, 4).'-'.substr($hex, 20);
    }
}
