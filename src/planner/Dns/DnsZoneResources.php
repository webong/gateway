<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\WebProxy\Models\WebProxyEndpoint;

final class DnsZoneResources
{
    /** @return array<string, mixed> */
    public static function endpoint(WebProxyEndpoint $zone): array
    {
        $metadata = is_array($zone->metadata) ? $zone->metadata : [];
        $gateway = is_array($metadata['_gateway'] ?? null) ? $metadata['_gateway'] : [];

        return [
            'id' => $zone->getKey(),
            'name' => $gateway['hostname'] ?? null,
            'nameservers' => $gateway['nameservers'] ?? [],
            'soa_email' => $gateway['soa_email'] ?? null,
            'ttl' => (int) ($gateway['ttl'] ?? 0),
            'records' => $gateway['records'] ?? [],
            'provider' => $gateway['provider'] ?? 'manual',
            'status' => $gateway['status'] ?? 'active',
            'active' => (bool) $zone->is_active,
            'last_error' => $gateway['last_error'] ?? null,
            'created_at' => $zone->created_at?->toAtomString(),
            'updated_at' => $zone->updated_at?->toAtomString(),
        ];
    }
}
