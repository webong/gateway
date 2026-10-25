<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;

/** Planner-owned authoritative zones shared with Spinner. */
final class DnsZoneCatalog
{
    public function __construct(private readonly DnsZoneStore $zones) {}

    /** @return list<array{name: string, nameservers: list<string>, soa_email?: string, ttl?: int}> */
    public function all(): array
    {
        $zones = [];
        foreach ($this->zones->all(activeOnly: true) as $zone) {
            $name = $this->canonical((string) $zone->key);
            if ($name !== '') {
                $zones[$name] = [
                    'name' => $name,
                    'nameservers' => array_values(array_filter($zone->document['nameservers'] ?? [], 'is_string')),
                    'soa_email' => is_string($zone->document['soa_email'] ?? null) ? $zone->document['soa_email'] : '',
                    'ttl' => max(0, (int) ($zone->document['ttl'] ?? 0)),
                    'records' => array_values(is_array($zone->document['records'] ?? null) ? $zone->document['records'] : []),
                ];
            }
        }

        ksort($zones);

        return array_values($zones);
    }

    public function resolve(?string $requested): string
    {
        $zones = $this->all();
        if ($zones === []) {
            throw new InvalidArgumentException('DNS zones are not configured. Create an active zone through POST /dns/zones.');
        }
        $requested = $this->canonical($requested ?? '');
        if ($requested === '') {
            if (count($zones) !== 1) {
                throw new InvalidArgumentException('Specify a DNS zone when more than one zone is configured.');
            }

            return $zones[0]['name'];
        }
        foreach ($zones as $zone) {
            if ($zone['name'] === $requested) {
                return $requested;
            }
        }

        throw new InvalidArgumentException('The requested DNS zone is not configured.');
    }

    private function canonical(string $name): string
    {
        return strtolower(rtrim(trim($name), '.'));
    }
}
