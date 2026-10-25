<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;
use LogicException;
use stdClass;
use Throwable;
use Webong\Gateway\RegistryRouteCache;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Proxy\Models\WebProxyEndpoint;

/** Planner-owned authoritative DNS zones stored as versioned desired state. */
final readonly class DnsZoneStore
{
    private const KIND = 'dns.zone';

    public function __construct(
        private PlannerResourceStore $resources,
        private DnsProvisionerRegistry $provisioners,
        private RegistryRouteCache $routeCache,
    ) {}

    /** @return list<stdClass> */
    public function all(bool $activeOnly = false): array
    {
        return array_values(array_filter(
            $this->resources->all(self::KIND),
            static fn (stdClass $zone): bool => ! $activeOnly
                || ($zone->enabled && ($zone->document['status'] ?? null) === 'active'),
        ));
    }

    public function find(string $identifier): ?stdClass
    {
        return $this->resources->find(self::KIND, $identifier)
            ?? $this->resources->byKey(self::KIND, $this->canonical($identifier));
    }

    /**
     * @param list<string> $nameservers
     * @param array<string, mixed> $provisionerOptions
     */
    public function create(
        string $name,
        array $nameservers,
        string $provisioner = 'manual',
        array $provisionerOptions = [],
        ?string $soaEmail = null,
        int $ttl = 60,
    ): stdClass {
        $name = $this->canonical($name);
        if (! $this->validName($name)) {
            throw new InvalidArgumentException('DNS zone must be a valid domain.');
        }
        if (! array_is_list($nameservers)
            || array_filter($nameservers, static fn (mixed $server): bool => ! is_string($server)) !== []) {
            throw new InvalidArgumentException('DNS zone nameservers must be a list of domains.');
        }
        $nameservers = array_values(array_map(fn (string $server): string => $this->canonical($server), $nameservers));
        if ($nameservers === [] || count($nameservers) > 8
            || count(array_unique($nameservers)) !== count($nameservers)
            || array_filter($nameservers, fn (string $server): bool => ! $this->validName($server)) !== []) {
            throw new InvalidArgumentException('DNS zone requires one to eight unique nameserver domains.');
        }
        $soaEmail = $soaEmail !== null ? $this->canonical($soaEmail) : null;
        if ($soaEmail !== null && ! $this->validName($soaEmail)) {
            throw new InvalidArgumentException('DNS SOA email must use DNS mailbox-name form.');
        }
        if ($ttl < 60 || $ttl > 86400) {
            throw new InvalidArgumentException('DNS zone TTL must be between 60 and 86400 seconds.');
        }

        $provisioner = strtolower(trim($provisioner));
        $this->provisioners->resolve($provisioner);
        $zone = $this->resources->create(self::KIND, $name, [
            'nameservers' => $nameservers,
            'soa_email' => $soaEmail,
            'ttl' => $ttl,
            'records' => [],
            'provisioner' => $provisioner,
            'provisioner_options' => $provisionerOptions,
            'provisioner_state' => [],
            'status' => 'provisioning',
            'operation' => 'provision',
            'last_error' => null,
        ], false);

        return $this->provision($zone, $zone->revision);
    }

    public function provision(stdClass $zone, int $revision): stdClass
    {
        $document = $zone->document;
        $provisioner = $this->provisioners->resolve((string) ($document['provisioner'] ?? 'manual'));
        $zone = $this->replaceState($zone, $revision, 'provisioning', false);
        try {
            $state = $provisioner->provision(
                $this->definition($zone),
                $this->array($zone->document['provisioner_options'] ?? []),
                $this->array($zone->document['provisioner_state'] ?? []),
            );
        } catch (Throwable $exception) {
            $failed = $this->replaceState($zone, $zone->revision, 'error', false, $exception->getMessage());
            throw new DnsZoneProvisioningFailed($failed, $exception->getMessage());
        }

        $document = $zone->document;
        $document['provisioner_state'] = $state;
        $document['status'] = 'active';
        $document['operation'] = 'provision';
        $document['last_error'] = null;
        $updated = $this->resources->replace(self::KIND, $zone->id, $zone->key, $document, true, $zone->revision);
        $this->routeCache->invalidatePaths();

        return $updated ?? throw new LogicException('DNS zone disappeared while it was being provisioned.');
    }

    /** Replace the complete operator-owned record set; NS and SOA stay zone-managed.
     * @param list<array<string, mixed>> $records
     */
    public function replaceRecords(stdClass $zone, array $records, int $revision): stdClass
    {
        $name = (string) $zone->key;
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

        $document = $zone->document;
        $document['records'] = $normalized;
        $updated = $this->resources->replace(self::KIND, $zone->id, $zone->key, $document, $zone->enabled, $revision);
        $this->routeCache->invalidatePaths();

        return $updated ?? throw new LogicException('DNS zone disappeared while its records were being replaced.');
    }

    public function delete(stdClass $zone, int $revision): void
    {
        if (($zone->document['records'] ?? []) !== []) {
            throw new LogicException('Remove the DNS zone records before deleting the zone.');
        }
        if (WebProxyEndpoint::query()
            ->where('metadata->_gateway->kind', DnsEndpointRegistry::KIND)
            ->where('metadata->_gateway->zone', $zone->key)
            ->where('metadata->_gateway->deleted', false)
            ->exists()) {
            throw new LogicException('Delete the DNS endpoints before removing the zone.');
        }

        $zone = $this->replaceState($zone, $revision, 'deleting', false, null, 'delete');
        try {
            $this->provisioners->resolve((string) ($zone->document['provisioner'] ?? 'manual'))->deprovision(
                $this->definition($zone),
                $this->array($zone->document['provisioner_state'] ?? []),
            );
        } catch (Throwable $exception) {
            $failed = $this->replaceState($zone, $zone->revision, 'error', false, $exception->getMessage(), 'delete');
            throw new DnsZoneProvisioningFailed($failed, $exception->getMessage());
        }
        $this->resources->delete(self::KIND, $zone->id, $zone->revision);
        $this->routeCache->invalidatePaths();
    }

    private function replaceState(
        stdClass $zone,
        int $revision,
        string $status,
        bool $enabled,
        ?string $error = null,
        string $operation = 'provision',
    ): stdClass {
        $document = $zone->document;
        $document['status'] = $status;
        $document['operation'] = $operation;
        $document['last_error'] = $error;
        $updated = $this->resources->replace(self::KIND, $zone->id, $zone->key, $document, $enabled, $revision);

        return $updated ?? throw new LogicException('DNS zone disappeared while its state was being replaced.');
    }

    /** @return array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string} */
    private function definition(stdClass $zone): array
    {
        return [
            'name' => (string) $zone->key,
            'nameservers' => array_values(array_filter($this->array($zone->document['nameservers'] ?? []), 'is_string')),
            'ttl' => max(0, (int) ($zone->document['ttl'] ?? 60)),
            'soa_email' => is_string($zone->document['soa_email'] ?? null) ? $zone->document['soa_email'] : null,
        ];
    }

    /** @return array<string, mixed> */
    private function array(mixed $value): array
    {
        return is_array($value) ? $value : [];
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
        $labels = explode('.', $name);
        if (count($labels) < 2) {
            return false;
        }
        foreach ($labels as $label) {
            if ($label === '' || strlen($label) > 63 || ! preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/', $label)) {
                return false;
            }
        }

        return true;
    }
}
