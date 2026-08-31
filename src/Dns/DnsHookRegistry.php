<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Database\ConnectionInterface;
use Illuminate\Support\Str;
use RuntimeException;
use Webong\Gateway\Dns\Models\DnsHook;
use Webong\Gateway\RegistryRouteCache;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;

final readonly class DnsHookRegistry
{
    public function __construct(
        private EndpointRegistry $endpoints,
        private ConnectionInterface $database,
        private RegistryRouteCache $routeCache,
    ) {}

    /** @param array<string, mixed> $metadata */
    public function ensure(
        string $externalId,
        ?string $name = null,
        array $metadata = [],
        ?string $registry = null,
    ): DnsHook {
        $existing = DnsHook::withTrashed()->where('external_id', $externalId)->first();
        if ($existing !== null) {
            if ($existing->trashed()) {
                $existing->restore();
                $existing->forceFill(['is_active' => true])->save();
                $this->routeCache->invalidatePaths();
            }

            return $existing->refresh();
        }

        $id = (string) Str::uuid();
        $token = $this->uniqueToken();
        $client = trim((string) config('gateway.dns.client', 'gateway-dns'));
        if ($client === '') {
            throw new RuntimeException('Gateway DNS hooks require a WebProxy client name.');
        }

        $endpoint = $this->endpoints->ensure(new EndpointDefinition(
            client: $client,
            externalId: $externalId,
            signingSecret: null,
            verificationToken: null,
            endpointKey: 'dns-'.$token,
            credentialOwnerId: null,
            managed: true,
            registry: $registry,
            metadata: [
                ...$metadata,
                '_gateway_dns_hook_id' => $id,
                '_gateway_dns_hook_token' => $token,
            ],
        ));

        $hook = $this->database->transaction(function () use ($id, $externalId, $name, $token, $endpoint, $metadata): DnsHook {
            return DnsHook::query()->create([
                'id' => $id,
                'external_id' => $externalId,
                'name' => $name,
                'token' => $token,
                'endpoint_key' => $endpoint->record->endpoint_key,
                'is_active' => true,
                'metadata' => $metadata,
            ]);
        });
        $this->routeCache->invalidatePaths();

        return $hook->refresh();
    }

    public function find(string $identifier, bool $withTrashed = false): ?DnsHook
    {
        $query = $withTrashed ? DnsHook::withTrashed() : DnsHook::query();

        return $query->where(function ($query) use ($identifier): void {
            $query->whereKey($identifier)
                ->orWhere('token', strtolower($identifier))
                ->orWhere('endpoint_key', $identifier);
        })->first();
    }

    /** @param array<string, mixed> $attributes */
    public function update(DnsHook $hook, array $attributes): DnsHook
    {
        $hook->fill($attributes);
        $hook->save();
        $this->routeCache->invalidatePaths();

        return $hook->refresh();
    }

    public function delete(DnsHook $hook): void
    {
        $hook->forceFill(['is_active' => false])->save();
        $hook->delete();
        $this->routeCache->invalidatePaths();
    }

    private function uniqueToken(): string
    {
        do {
            $token = bin2hex(random_bytes(16));
        } while (DnsHook::withTrashed()->where('token', $token)->exists());

        return $token;
    }
}
