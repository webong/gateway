<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Support\Collection;
use InvalidArgumentException;
use LogicException;
use Throwable;
use Webong\Gateway\RegistryRouteCache;
use Webong\WebProxy\EndpointDefinition;
use Webong\WebProxy\EndpointRegistry;
use Webong\WebProxy\Models\WebProxyEndpoint;

final readonly class DnsZoneRegistry
{
    public function __construct(
        private EndpointRegistry $endpoints,
        private DnsProviderRegistry $providers,
        private RegistryRouteCache $routeCache,
    ) {}

    /** @return Collection<int, WebProxyEndpoint> */
    public function all(bool $activeOnly = false): Collection
    {
        return WebProxyEndpoint::query()
            ->where('client', $this->client())
            ->where('metadata->_gateway->kind', 'dns_zone')
            ->where('metadata->_gateway->deleted', false)
            ->when($activeOnly, fn ($query) => $query->where('is_active', true)->where('metadata->_gateway->status', 'active'))
            ->orderBy('metadata->_gateway->hostname')
            ->get();
    }

    public function find(string $identifier): ?WebProxyEndpoint
    {
        $name = $this->canonical($identifier);

        return WebProxyEndpoint::query()
            ->where('client', $this->client())
            ->where('metadata->_gateway->kind', 'dns_zone')
            ->where('metadata->_gateway->deleted', false)
            ->where(function ($query) use ($identifier, $name): void {
                $query->whereKey($identifier)
                    ->orWhere('endpoint_key', $identifier)
                    ->orWhere('metadata->_gateway->hostname', $name);
            })
            ->first();
    }

    /**
     * @param list<string> $nameservers
     * @param array<string, mixed> $providerOptions
     */
    public function create(
        string $name,
        array $nameservers,
        string $provider = 'manual',
        array $providerOptions = [],
        ?string $soaEmail = null,
        int $ttl = 60,
        ?string $registry = null,
    ): WebProxyEndpoint {
        $name = $this->canonical($name);
        if (! $this->validName($name)) {
            throw new InvalidArgumentException('DNS zone must be a valid domain.');
        }
        $nameservers = array_values(array_map(fn (string $server): string => $this->canonical($server), $nameservers));
        if ($nameservers === [] || array_filter($nameservers, fn (string $server): bool => ! $this->validName($server)) !== []) {
            throw new InvalidArgumentException('DNS zone nameservers must be valid domains.');
        }
        $soaEmail = $soaEmail !== null ? $this->canonical($soaEmail) : null;
        if ($soaEmail !== null && ! $this->validName($soaEmail)) {
            throw new InvalidArgumentException('DNS SOA email must use DNS mailbox-name form.');
        }
        if (($existing = $this->find($name)) !== null) {
            return $existing;
        }

        $provider = strtolower(trim($provider));
        $this->providers->resolve($provider);
        $endpoint = $this->endpoints->ensure(new EndpointDefinition(
            client: $this->client(),
            externalId: 'dns-zone:'.$name,
            signingSecret: null,
            verificationToken: null,
            endpointKey: 'dns-zone-'.substr(hash('sha256', $name), 0, 24),
            credentialOwnerId: null,
            managed: true,
            registry: $registry,
            metadata: $this->metadata($name, $nameservers, $soaEmail, $ttl, $provider, $providerOptions, 'provisioning'),
        ));
        $zone = WebProxyEndpoint::query()->findOrFail($endpoint->record->id);

        return $this->provision($zone);
    }

    public function provision(WebProxyEndpoint $zone): WebProxyEndpoint
    {
        $gateway = $this->gateway($zone);
        $providerName = (string) ($gateway['provider'] ?? 'manual');
        $provider = $this->providers->resolve($providerName);
        $zone->update(['is_active' => false, 'metadata' => $this->withState($gateway, 'provisioning')]);
        try {
            $state = $provider->provision($this->definition($gateway), $this->array($gateway['provider_options'] ?? []));
        } catch (Throwable $exception) {
            $zone->update(['metadata' => $this->withState($gateway, 'error', $exception->getMessage())]);
            throw new DnsZoneProvisioningFailed($zone->refresh(), $exception->getMessage());
        }

        $gateway['provider_state'] = $state;
        $zone->update(['is_active' => true, 'metadata' => $this->withState($gateway, 'active')]);
        $this->routeCache->invalidatePaths();

        return $zone->refresh();
    }

    public function delete(WebProxyEndpoint $zone): void
    {
        $gateway = $this->gateway($zone);
        $children = WebProxyEndpoint::query()
            ->where('client', $this->client())
            ->where('metadata->_gateway->kind', 'dns_hook')
            ->where('metadata->_gateway->parent_endpoint_key', $zone->endpoint_key)
            ->where('metadata->_gateway->deleted', false)
            ->exists();
        if ($children) {
            throw new LogicException('Delete the DNS zone hooks before removing the zone.');
        }

        $zone->update(['is_active' => false, 'metadata' => $this->withState($gateway, 'deleting')]);
        try {
            $this->providers->resolve((string) ($gateway['provider'] ?? 'manual'))->deprovision(
                $this->definition($gateway),
                $this->array($gateway['provider_state'] ?? []),
            );
        } catch (Throwable $exception) {
            $zone->update(['metadata' => $this->withState($gateway, 'error', $exception->getMessage(), 'delete')]);
            throw new DnsZoneProvisioningFailed($zone->refresh(), $exception->getMessage());
        }
        $gateway['deleted'] = true;
        $zone->update(['metadata' => $this->withState($gateway, 'deleted')]);
        $this->routeCache->invalidatePaths();
    }

    /** @return array<string, mixed> */
    public function gateway(WebProxyEndpoint $zone): array
    {
        $metadata = $zone->metadata;
        $gateway = is_array($metadata) ? ($metadata['_gateway'] ?? []) : [];

        return is_array($gateway) ? $gateway : [];
    }

    /** @param list<string> $nameservers
     *  @param array<string, mixed> $providerOptions
     *  @return array<string, mixed>
     */
    private function metadata(string $name, array $nameservers, ?string $soaEmail, int $ttl, string $provider, array $providerOptions, string $status): array
    {
        return ['_gateway' => [
            'kind' => 'dns_zone',
            'protocol' => 'dns',
            'hostname' => $name,
            'nameservers' => array_values($nameservers),
            'soa_email' => $soaEmail,
            'ttl' => $ttl,
            'provider' => $provider,
            'provider_options' => $providerOptions,
            'provider_state' => [],
            'status' => $status,
            'operation' => 'provision',
            'last_error' => null,
            'deleted' => false,
        ]];
    }

    /** @param array<string, mixed> $gateway
     *  @return array<string, mixed>
     */
    private function withState(array $gateway, string $status, ?string $error = null, string $operation = 'provision'): array
    {
        $gateway['status'] = $status;
        $gateway['operation'] = $operation;
        $gateway['last_error'] = $error;

        return ['_gateway' => $gateway];
    }

    /** @param array<string, mixed> $gateway
     *  @return array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string}
     */
    private function definition(array $gateway): array
    {
        return [
            'name' => (string) ($gateway['hostname'] ?? ''),
            'nameservers' => array_values(array_filter($this->array($gateway['nameservers'] ?? []), 'is_string')),
            'ttl' => max(0, (int) ($gateway['ttl'] ?? 60)),
            'soa_email' => is_string($gateway['soa_email'] ?? null) ? $gateway['soa_email'] : null,
        ];
    }

    /** @return array<string, mixed> */
    private function array(mixed $value): array
    {
        return is_array($value) ? $value : [];
    }

    private function client(): string
    {
        return trim((string) config('gateway.dns.client', 'gateway'));
    }

    private function canonical(string $name): string
    {
        return strtolower(rtrim(trim($name), '.'));
    }

    private function validName(string $name): bool
    {
        if ($name === '' || strlen($name) > 253) {
            return false;
        }
        foreach (explode('.', $name) as $label) {
            if ($label === '' || strlen($label) > 63 || ! preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/', $label)) {
                return false;
            }
        }

        return true;
    }
}
