<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use RuntimeException;
use Webong\Gateway\RegistryRouteCache;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;
use Webong\WebProxy\Models\WebProxyEndpoint;

final readonly class DnsHookRegistry
{
    public function __construct(
        private EndpointRegistry $endpoints,
        private RegistryRouteCache $routeCache,
    ) {}

    /** @param array<string, mixed> $metadata */
    public function ensure(
        string $externalId,
        ?string $name = null,
        array $metadata = [],
        ?string $registry = null,
    ): WebProxyEndpoint {
        $client = $this->client();
        if ($client === '') {
            throw new RuntimeException('Gateway DNS hooks require a WebProxy client name.');
        }

        $existing = WebProxyEndpoint::query()
            ->where('client', $client)
            ->where('external_id', 'dns-hook:'.$externalId)
            ->first();
        if ($existing !== null) {
            $gateway = $this->gateway($existing);
            if (($gateway['kind'] ?? null) !== 'dns_hook') {
                throw new RuntimeException('The DNS hook identity is already used by another Gateway endpoint.');
            }
            if (! $existing->is_active || ($gateway['deleted'] ?? false)) {
                $existing->update([
                    'is_active' => true,
                    'metadata' => $this->metadata(
                        externalId: (string) ($gateway['external_id'] ?? $externalId),
                        token: (string) ($gateway['token'] ?? ''),
                        name: is_string($gateway['name'] ?? null) ? $gateway['name'] : null,
                        metadata: is_array($gateway['metadata'] ?? null) ? $gateway['metadata'] : [],
                        zone: (string) ($gateway['zone'] ?? $this->zone()),
                        parentEndpointKey: (string) ($gateway['parent_endpoint_key'] ?? ''),
                    ),
                ]);
                $this->routeCache->invalidatePaths();
            }

            return $existing->refresh();
        }

        $token = $this->uniqueToken();
        $zone = $this->zone();
        $zoneEndpoint = $this->ensureZone($client, $zone, $registry);
        $endpoint = $this->endpoints->ensure(new EndpointDefinition(
            client: $client,
            externalId: 'dns-hook:'.$externalId,
            signingSecret: null,
            verificationToken: null,
            endpointKey: 'dns-'.$token,
            credentialOwnerId: null,
            managed: true,
            registry: $registry,
            metadata: $this->metadata($externalId, $token, $name, $metadata, $zone, $zoneEndpoint->endpoint_key),
        ));
        $this->routeCache->invalidatePaths();

        return WebProxyEndpoint::query()->findOrFail($endpoint->record->id);
    }

    public function find(string $identifier): ?WebProxyEndpoint
    {
        $token = strtolower($identifier);

        return WebProxyEndpoint::query()
            ->where('client', $this->client())
            ->where('metadata->_gateway->kind', 'dns_hook')
            ->where('metadata->_gateway->deleted', false)
            ->where(function ($query) use ($identifier, $token): void {
            $query->whereKey($identifier)
                ->orWhere('endpoint_key', $identifier)
                ->orWhere('metadata->_gateway->token', $token)
                ->orWhere('metadata->_gateway->external_id', $identifier);
            })
            ->first();
    }

    /** @param array<string, mixed> $attributes */
    public function update(WebProxyEndpoint $hook, array $attributes): WebProxyEndpoint
    {
        $gateway = $this->gateway($hook);
        $metadata = $attributes['metadata'] ?? ($gateway['metadata'] ?? []);
        $name = array_key_exists('name', $attributes) ? $attributes['name'] : ($gateway['name'] ?? null);
        $hook->update([
            'is_active' => $attributes['is_active'] ?? $hook->is_active,
            'metadata' => $this->metadata(
                externalId: (string) ($gateway['external_id'] ?? $hook->external_id),
                token: (string) ($gateway['token'] ?? ''),
                name: is_string($name) ? $name : null,
                metadata: is_array($metadata) ? $metadata : [],
                zone: (string) ($gateway['zone'] ?? $this->zone()),
                parentEndpointKey: (string) ($gateway['parent_endpoint_key'] ?? ''),
            ),
        ]);
        $this->routeCache->invalidatePaths();

        return $hook->refresh();
    }

    public function delete(WebProxyEndpoint $hook): void
    {
        $gateway = $this->gateway($hook);
        $hook->update([
            'is_active' => false,
            'metadata' => $this->metadata(
                externalId: (string) ($gateway['external_id'] ?? $hook->external_id),
                token: (string) ($gateway['token'] ?? ''),
                name: is_string($gateway['name'] ?? null) ? $gateway['name'] : null,
                metadata: is_array($gateway['metadata'] ?? null) ? $gateway['metadata'] : [],
                zone: (string) ($gateway['zone'] ?? $this->zone()),
                parentEndpointKey: (string) ($gateway['parent_endpoint_key'] ?? ''),
                deleted: true,
            ),
        ]);
        $this->routeCache->invalidatePaths();
    }

    private function uniqueToken(): string
    {
        do {
            $token = bin2hex(random_bytes(16));
        } while (WebProxyEndpoint::query()->where('endpoint_key', 'dns-'.$token)->exists());

        return $token;
    }

    private function client(): string
    {
        return trim((string) config('gateway.dns.client', 'gateway'));
    }

    private function zone(): string
    {
        return strtolower(trim((string) config('gateway.dns.zone', ''), '.'));
    }

    private function ensureZone(string $client, string $zone, ?string $registry): WebProxyEndpoint
    {
        $endpoint = $this->endpoints->ensure(new EndpointDefinition(
            client: $client,
            externalId: 'dns-zone:'.$zone,
            signingSecret: null,
            verificationToken: null,
            endpointKey: 'dns-zone-'.substr(hash('sha256', $zone), 0, 24),
            credentialOwnerId: null,
            managed: true,
            registry: $registry,
            metadata: ['_gateway' => [
                'kind' => 'dns_zone',
                'protocol' => 'dns',
                'hostname' => $zone,
            ]],
        ));

        return WebProxyEndpoint::query()->findOrFail($endpoint->record->id);
    }

    /** @param array<string, mixed> $metadata
     *  @return array<string, mixed>
     */
    private function metadata(
        string $externalId,
        string $token,
        ?string $name,
        array $metadata,
        string $zone,
        string $parentEndpointKey,
        bool $deleted = false,
    ): array {
        return ['_gateway' => [
            'kind' => 'dns_hook',
            'protocol' => 'dns',
            'hostname' => $token.'.'.$zone,
            'parent_endpoint_key' => $parentEndpointKey,
            'external_id' => $externalId,
            'token' => $token,
            'name' => $name,
            'routing' => ['scope' => 'dns', 'key' => 'query'],
            'metadata' => $metadata,
            'zone' => $zone,
            'deleted' => $deleted,
        ]];
    }

    /** @return array<string, mixed> */
    public function gateway(WebProxyEndpoint $endpoint): array
    {
        $metadata = $endpoint->metadata;
        $gateway = is_array($metadata) ? ($metadata['_gateway'] ?? []) : [];

        return is_array($gateway) ? $gateway : [];
    }
}
