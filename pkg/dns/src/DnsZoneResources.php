<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use stdClass;

final class DnsZoneResources
{
    /** @return array<string, mixed> */
    public static function zone(stdClass $zone): array
    {
        return [
            'id' => $zone->id,
            'name' => $zone->key,
            'mode' => 'managed',
            'nameservers' => $zone->document['nameservers'] ?? [],
            'soa_email' => $zone->document['soa_email'] ?? null,
            'ttl' => (int) ($zone->document['ttl'] ?? 0),
            'records' => $zone->document['records'] ?? [],
            'provisioner' => $zone->document['provisioner'] ?? 'manual',
            'status' => $zone->document['status'] ?? 'active',
            'enabled' => $zone->enabled,
            'revision' => $zone->revision,
            'last_error' => $zone->document['last_error'] ?? null,
            'events_url' => '/dns/zones/'.$zone->id.'/events',
            'usage_url' => '/dns/zones/'.$zone->id.'/usage',
            'created_at' => $zone->created_at,
            'updated_at' => $zone->updated_at,
        ];
    }
}
