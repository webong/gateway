<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;

/** Configured authoritative zones shared with Spinner, not a second identity store. */
final class DnsZoneCatalog
{
    public function __construct(private readonly DnsZoneRegistry $registry) {}

    /** @return list<array{name: string, nameservers: list<string>, soa_email?: string, ttl?: int}> */
    public function all(): array
    {
        $zones = [];
        foreach ($this->configured() as $zone) {
            $zones[$zone['name']] = $zone;
        }
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

    /** @return list<array{name: string, nameservers: list<string>, soa_email?: string, ttl?: int}> */
    private function configured(): array
    {
        $configured = config('gateway.dns.zones', []);
        if (! is_array($configured) || ! array_is_list($configured)) {
            throw new InvalidArgumentException('Gateway DNS zones must be a JSON array.');
        }
        $legacy = $this->canonical((string) config('gateway.dns.zone', ''));
        if ($configured !== [] && $legacy !== '') {
            throw new InvalidArgumentException('Gateway DNS zones cannot be combined with the single-zone setting.');
        }
        if ($configured === []) {
            if ($legacy === '') {
                return [];
            }
            if (! $this->validName($legacy)) {
                throw new InvalidArgumentException('Gateway DNS zone must be a valid domain.');
            }
            $nameservers = config('gateway.dns.nameservers', []);

            return [['name' => $legacy, 'nameservers' => is_array($nameservers) ? array_values($nameservers) : []]];
        }

        $zones = [];
        foreach ($configured as $entry) {
            if (! is_array($entry) || ! is_string($entry['name'] ?? null) || ! is_array($entry['nameservers'] ?? null)
                || ! array_is_list($entry['nameservers']) || $entry['nameservers'] === []) {
                throw new InvalidArgumentException('Each Gateway DNS zone needs a name and nameservers.');
            }
            $name = $this->canonical($entry['name']);
            if (! $this->validName($name) || isset($zones[$name])) {
                throw new InvalidArgumentException('Gateway DNS zone names must be unique valid domains.');
            }
            $nameservers = [];
            foreach ($entry['nameservers'] as $nameserver) {
                if (! is_string($nameserver) || ! $this->validName($this->canonical($nameserver))) {
                    throw new InvalidArgumentException('Gateway DNS nameservers must be valid domains.');
                }
                $nameservers[] = $this->canonical($nameserver);
            }
            $zones[$name] = ['name' => $name, 'nameservers' => $nameservers];
        }

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
