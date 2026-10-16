<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;

/** Planner-owned authoritative zones shared with Spinner. */
final class DnsZoneCatalog
{
    public function __construct(private readonly DnsZoneRegistry $registry) {}

    /** @return list<array{name: string, nameservers: list<string>, soa_email?: string, ttl?: int}> */
    public function all(): array
    {
        $zones = [];
        foreach ($this->registry->all(activeOnly: true) as $endpoint) {
            $gateway = $this->registry->gateway($endpoint);
            $name = $this->canonical((string) ($gateway['hostname'] ?? ''));
            if ($name !== '') {
                $zones[$name] = [
                    'name' => $name,
                    'nameservers' => array_values(array_filter($gateway['nameservers'] ?? [], 'is_string')),
                    'soa_email' => is_string($gateway['soa_email'] ?? null) ? $gateway['soa_email'] : '',
                    'ttl' => max(0, (int) ($gateway['ttl'] ?? 0)),
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
            throw new InvalidArgumentException('DNS hooks are not configured. Create an active zone through POST /dns/zones.');
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
