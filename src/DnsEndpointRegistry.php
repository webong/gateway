<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use RuntimeException;
use Webong\Gateway\RegistryRouteCache;
use Webong\Gateway\Resources\StaleResource;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EndpointRegistry;
use Webong\Proxy\Models\WebProxyEndpoint;

final readonly class DnsEndpointRegistry
{
    public const string CLIENT = 'gateway';

    public const string KIND = 'dns_endpoint';

    public function __construct(
        private EndpointRegistry $endpoints,
        private RegistryRouteCache $routeCache,
        private DnsZoneCatalog $zones,
    ) {}

    /** @param array<string, mixed> $metadata */
    public function ensure(
        string $externalId,
        ?string $name = null,
        array $metadata = [],
        ?string $registry = null,
        ?string $requestedZone = null,
    ): WebProxyEndpoint {
        $client = $this->client();
        if ($client === '') {
            throw new RuntimeException('Gateway DNS endpoints require a WebProxy client name.');
        }

        $zone = $this->zones->resolve($requestedZone);
        $existing = WebProxyEndpoint::query()
            ->where('client', $client)
            ->where('metadata->_gateway->kind', self::KIND)
            ->where('metadata->_gateway->zone', $zone)
            ->where('metadata->_gateway->external_id', $externalId)
            ->first();
        if ($existing !== null) {
            $gateway = $this->gateway($existing);
            if (($gateway['kind'] ?? null) !== self::KIND) {
                throw new RuntimeException('The DNS endpoint identity is already used by another Gateway endpoint.');
            }
            if (! $existing->is_active || ($gateway['deleted'] ?? false)) {
                $existing->update([
                    'is_active' => true,
                    'metadata' => $this->metadata(
                        externalId: (string) ($gateway['external_id'] ?? $externalId),
                        token: (string) ($gateway['token'] ?? ''),
                        name: is_string($gateway['name'] ?? null) ? $gateway['name'] : null,
                        metadata: is_array($gateway['metadata'] ?? null) ? $gateway['metadata'] : [],
                        zone: $zone,
                        revision: max(1, (int) ($gateway['revision'] ?? 1)) + 1,
                    ),
                ]);
                $this->routeCache->invalidatePaths();
            }

            return $existing->refresh();
        }

        $token = $this->uniqueToken();
        $endpoint = $this->endpoints->ensure(new EndpointDefinition(
            client: $client,
            externalId: 'dns-endpoint:'.$zone.':'.$externalId,
            signingSecret: null,
            verificationToken: null,
            endpointKey: 'dns-'.$token,
            credentialOwnerId: null,
            managed: true,
            registry: $registry,
            metadata: $this->metadata($externalId, $token, $name, $metadata, $zone),
        ));
        $this->routeCache->invalidatePaths();

        return WebProxyEndpoint::query()->findOrFail($endpoint->record->id);
    }

    public function find(string $identifier, ?string $zone = null): ?WebProxyEndpoint
    {
        $token = strtolower($identifier);
        $zone = $zone !== null ? $this->zones->resolve($zone) : null;

        $matches = WebProxyEndpoint::query()
            ->where('client', $this->client())
            ->where('metadata->_gateway->kind', self::KIND)
            ->where('metadata->_gateway->deleted', false)
            ->when($zone !== null, fn ($query) => $query->where('metadata->_gateway->zone', $zone))
            ->where(function ($query) use ($identifier, $token): void {
                $query->whereKey($identifier)
                    ->orWhere('endpoint_key', $identifier)
                    ->orWhere('metadata->_gateway->token', $token)
                    ->orWhere('metadata->_gateway->external_id', $identifier);
            })
            ->limit(2)->get();

        return $matches->count() === 1 ? $matches->first() : null;
    }

    /** @param array<string, mixed> $attributes */
    public function update(WebProxyEndpoint $endpoint, array $attributes, int $revision): WebProxyEndpoint
    {
        $gateway = $this->gateway($endpoint);
        if ((int) ($gateway['revision'] ?? 1) !== $revision) {
            throw new StaleResource('DNS endpoint revision has changed.');
        }
        $metadata = $attributes['metadata'] ?? ($gateway['metadata'] ?? []);
        $name = array_key_exists('name', $attributes) ? $attributes['name'] : ($gateway['name'] ?? null);
        $endpoint->update([
            'is_active' => $attributes['is_active'] ?? $endpoint->is_active,
            'metadata' => $this->metadata(
                externalId: (string) ($gateway['external_id'] ?? $endpoint->external_id),
                token: (string) ($gateway['token'] ?? ''),
                name: is_string($name) ? $name : null,
                metadata: is_array($metadata) ? $metadata : [],
                zone: (string) ($gateway['zone'] ?? $this->zones->resolve(null)),
                revision: $revision + 1,
            ),
        ]);
        $this->routeCache->invalidatePaths();

        return $endpoint->refresh();
    }

    public function delete(WebProxyEndpoint $endpoint, int $revision): void
    {
        $gateway = $this->gateway($endpoint);
        if ((int) ($gateway['revision'] ?? 1) !== $revision) {
            throw new StaleResource('DNS endpoint revision has changed.');
        }
        $endpoint->update([
            'is_active' => false,
            'metadata' => $this->metadata(
                externalId: (string) ($gateway['external_id'] ?? $endpoint->external_id),
                token: (string) ($gateway['token'] ?? ''),
                name: is_string($gateway['name'] ?? null) ? $gateway['name'] : null,
                metadata: is_array($gateway['metadata'] ?? null) ? $gateway['metadata'] : [],
                zone: (string) ($gateway['zone'] ?? $this->zones->resolve(null)),
                deleted: true,
                revision: $revision + 1,
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
        return self::CLIENT;
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
        bool $deleted = false,
        int $revision = 1,
    ): array {
        return ['_gateway' => [
            'kind' => self::KIND,
            'protocol' => 'dns',
            'hostname' => $token.'.'.$zone,
            'external_id' => $externalId,
            'token' => $token,
            'name' => $name,
            'routing' => ['scope' => 'dns', 'key' => 'query'],
            'metadata' => $metadata,
            'zone' => $zone,
            'deleted' => $deleted,
            'revision' => $revision,
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
