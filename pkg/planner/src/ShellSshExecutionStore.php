<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Safe identity is versioned; live private material is deliberately not. */
final readonly class ShellSshExecutionStore
{
    private const KIND = 'shell.ssh-execution';

    public function __construct(private PlannerResourceStore $resources) {}

    /** @param array<string, mixed> $ssh */
    public function create(string $endpointKey, string $subscriberId, string $hook, array $ssh): stdClass
    {
        if (array_diff(array_keys($ssh), ['host', 'port', 'user', 'host_public_key', 'private_key']) !== []) {
            throw new InvalidArgumentException('SSH configuration contains unsupported properties.');
        }
        $host = $ssh['host'] ?? null;
        $port = $ssh['port'] ?? 22;
        $user = $ssh['user'] ?? null;
        $hostKey = $ssh['host_public_key'] ?? null;
        $privateKey = $ssh['private_key'] ?? null;
        if (! is_string($host) || strlen($host) > 253
            || preg_match('/^[A-Za-z0-9][A-Za-z0-9.-]*$/D', $host) !== 1
            || ! is_int($port) || $port < 1 || $port > 65535
            || ! is_string($user) || preg_match('/^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/D', $user) !== 1
            || ! is_string($hostKey) || strlen($hostKey) > 8192
            || preg_match('/^ssh-ed25519 [A-Za-z0-9+\/]+={0,2}$/D', $hostKey) !== 1
            || ! is_string($privateKey)) {
            throw new InvalidArgumentException('SSH configuration is invalid.');
        }
        self::assertPrivateKey($privateKey);
        ShellSubscriptionTarget::assertHook($hook);

        return DB::transaction(function () use ($endpointKey, $subscriberId, $hook, $host, $port, $user, $hostKey, $privateKey): stdClass {
            $row = $this->resources->create(self::KIND, (string) Str::uuid(), [
                'endpoint_key' => $endpointKey, 'subscriber_id' => $subscriberId,
                'hook' => $hook, 'host' => $host, 'port' => $port,
                'user' => $user, 'host_public_key' => $hostKey,
                'credential_version' => 1,
            ]);
            DB::table('configuration_secrets')->insert([
                'resource_id' => $row->id, 'ciphertext' => Crypt::encryptString($privateKey),
                'version' => 1, 'created_at' => now('UTC'), 'updated_at' => now('UTC'),
            ]);

            return $row;
        });
    }

    public function findForPlan(string $id, string $endpointKey, string $subscriberId, string $hook): ?stdClass
    {
        $row = $this->resources->find(self::KIND, $id);
        if ($row === null || ! $row->enabled || $row->document['endpoint_key'] !== $endpointKey
            || $row->document['subscriber_id'] !== $subscriberId || $row->document['hook'] !== $hook
            || ! DB::table('configuration_secrets')->where('resource_id', $id)->exists()) {
            return null;
        }

        return $row;
    }

    /** @return array<string, mixed>|null */
    public function publicIdentity(string $id): ?array
    {
        $row = $this->resources->find(self::KIND, $id);
        if ($row === null || ! $row->enabled) {
            return null;
        }

        return [
            'host' => $row->document['host'], 'port' => $row->document['port'],
            'user' => $row->document['user'], 'host_public_key' => $row->document['host_public_key'],
            'credential_version' => (int) DB::table('configuration_secrets')->where('resource_id', $id)->value('version'),
        ];
    }

    /** @return array<string, mixed>|null */
    public function resolve(string $id): ?array
    {
        $row = $this->resources->find(self::KIND, $id);
        $secret = $row === null ? null : DB::table('configuration_secrets')->where('resource_id', $id)->first();
        if ($row === null || ! $row->enabled || $secret === null) {
            return null;
        }

        return [
            'hook' => $row->document['hook'], 'host' => $row->document['host'],
            'port' => $row->document['port'], 'user' => $row->document['user'],
            'host_public_key' => $row->document['host_public_key'],
            'private_key' => Crypt::decryptString($secret->ciphertext),
            'credential_version' => (int) $secret->version,
        ];
    }

    public function rotate(string $id, string $privateKey): void
    {
        self::assertPrivateKey($privateKey);
        DB::transaction(function () use ($id, $privateKey): void {
            $row = $this->resources->find(self::KIND, $id);
            if ($row === null || ! $row->enabled) {
                throw new InvalidArgumentException('The SSH Shell execution is unavailable.');
            }
            $updated = DB::table('configuration_secrets')->where('resource_id', $id)
                ->increment('version', 1, ['ciphertext' => Crypt::encryptString($privateKey), 'updated_at' => now('UTC')]);
            if ($updated !== 1) {
                throw new InvalidArgumentException('The SSH Shell execution is unavailable.');
            }
            $this->resources->replace(self::KIND, $id, $row->key,
                [...$row->document, 'credential_version' => ($row->document['credential_version'] ?? 1) + 1],
                true, $row->revision);
        });
    }

    public function revoke(string $id): void
    {
        $row = $this->resources->find(self::KIND, $id);
        if ($row === null || ! $row->enabled) {
            return;
        }
        $this->resources->replace(self::KIND, $id, $row->key, $row->document, false, $row->revision);
        DB::table('configuration_secrets')->where('resource_id', $id)->delete();
    }

    public static function assertPrivateKey(string $privateKey): void
    {
        if (strlen($privateKey) > 16384
            || ! str_starts_with($privateKey, '-----BEGIN OPENSSH PRIVATE KEY-----')
            || ! str_contains($privateKey, '-----END OPENSSH PRIVATE KEY-----')) {
            throw new InvalidArgumentException('SSH private key is invalid.');
        }
    }
}
