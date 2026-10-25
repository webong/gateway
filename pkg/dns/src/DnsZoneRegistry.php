<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Support\Collection;
use InvalidArgumentException;
use LogicException;
use Throwable;
use Webong\Gateway\RegistryRouteCache;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EndpointRegistry;
use Webong\Proxy\Models\WebProxyEndpoint;

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

    /** Replace the complete operator-owned record set; NS and SOA stay zone-managed. */
    public function replaceRecords(WebProxyEndpoint $zone, array $records): WebProxyEndpoint
    {
        $gateway = $this->gateway($zone);
        $name = (string) ($gateway['hostname'] ?? '');
        if (! array_is_list($records) || count($records) > 256) {
            throw new InvalidArgumentException('DNS records must be a list of at most 256 entries.');
        }
        $normalized = [];
        $byName = [];
        foreach ($records as $record) {
            if (! is_array($record) || array_diff(array_keys($record), ['name', 'type', 'value', 'preference']) !== []
                || ! is_string($record['name'] ?? null) || ! is_string($record['type'] ?? null)
                || ! is_string($record['value'] ?? null)) {
                throw new InvalidArgumentException('DNS record properties are invalid.');
            }
            $owner = $this->canonical($record['name']);
            $type = strtoupper($record['type']);
            $value = trim($record['value']);
            if (! $this->validRecordOwner($owner) || ($owner !== $name && ! str_ends_with($owner, '.'.$name))
                || ! in_array($type, ['A', 'AAAA', 'CNAME', 'MX', 'TXT'], true)) {
                throw new InvalidArgumentException('DNS record owner or type is invalid for this zone.');
            }
            if ($type === 'A' && filter_var($value, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false
                || $type === 'AAAA' && filter_var($value, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) === false
                || in_array($type, ['CNAME', 'MX'], true) && $value !== '.' && ! $this->validName($this->canonical($value))
                || $type === 'CNAME' && $value === '.'
                || $type === 'TXT' && (strlen($value) > 4096 || preg_match('/[\x00-\x1f\x7f]/', $value) === 1)) {
                throw new InvalidArgumentException('DNS record value is invalid.');
            }
            $preference = $record['preference'] ?? null;
            if ($type === 'MX') {
                if (! is_int($preference) || $preference < 0 || $preference > 65535
                    || ($value === '.' && $preference !== 0)) {
                    throw new InvalidArgumentException('DNS MX preference is invalid.');
                }
            } elseif ($preference !== null) {
                throw new InvalidArgumentException('Only MX records accept preference.');
            }
            $byName[$owner][] = $type;
            $normalized[] = ['name' => $owner, 'type' => $type, 'value' => $value]
                + ($type === 'MX' ? ['preference' => $preference] : []);
        }
        foreach ($byName as $types) {
            if (count($types) > 16) {
                throw new InvalidArgumentException('A DNS name may have at most 16 records.');
            }
            if (in_array('CNAME', $types, true) && count($types) !== 1) {
                throw new InvalidArgumentException('CNAME must be the only record at its name.');
            }
        }
        if (in_array('CNAME', $byName[$name] ?? [], true)) {
            throw new InvalidArgumentException('A zone apex cannot be a CNAME.');
        }
        $gateway['records'] = $normalized;
        $zone->update(['metadata' => ['_gateway' => $gateway]]);
        return $zone->refresh();
    }

    public function delete(WebProxyEndpoint $zone): void
    {
        $gateway = $this->gateway($zone);
        if (($gateway['records'] ?? []) !== []) {
            throw new LogicException('Remove the DNS zone records before deleting the zone.');
        }
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

    private function validRecordOwner(string $name): bool
    {
        return strlen($name) <= 253 && preg_match('/\A(?:[a-z0-9_][a-z0-9_-]{0,62}\.)+[a-z0-9][a-z0-9-]{0,62}\z/D', $name) === 1;
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
