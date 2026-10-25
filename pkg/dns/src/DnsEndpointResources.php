<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Proxy\Models\WebProxyEndpoint;

final class DnsEndpointResources
{
    /** @return array<string, mixed> */
    public static function endpoint(WebProxyEndpoint $endpoint): array
    {
        $gateway = self::gateway($endpoint);
        $token = (string) ($gateway['token'] ?? '');
        $hostname = (string) ($gateway['hostname'] ?? $token);

        return [
            'id' => $endpoint->getKey(),
            'external_id' => $gateway['external_id'] ?? $endpoint->external_id,
            'name' => $gateway['name'] ?? null,
            'mode' => 'provided',
            'token' => $token,
            'hostname' => $hostname,
            'zone' => $gateway['zone'] ?? null,
            'query_name_template' => '{data}.'.$hostname,
            'endpoint_key' => $endpoint->endpoint_key,
            'enabled' => $endpoint->is_active,
            'revision' => max(1, (int) ($gateway['revision'] ?? 1)),
            'metadata' => $gateway['metadata'] ?? (object) [],
            'subscriptions_url' => '/registry/endpoints/'.$endpoint->endpoint_key.'/subscriptions',
            'events_url' => '/dns/endpoints/'.$endpoint->getKey().'/events',
            'usage_url' => '/dns/endpoints/'.$endpoint->getKey().'/usage',
            'created_at' => $endpoint->created_at?->toAtomString(),
            'updated_at' => $endpoint->updated_at?->toAtomString(),
        ];
    }

    /** @return array<string, mixed> */
    private static function gateway(WebProxyEndpoint $endpoint): array
    {
        $metadata = $endpoint->metadata;
        $gateway = is_array($metadata) ? ($metadata['_gateway'] ?? []) : [];

        return is_array($gateway) ? $gateway : [];
    }
}
