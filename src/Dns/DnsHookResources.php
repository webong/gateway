<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\WebProxy\Models\WebProxyEndpoint;

final class DnsHookResources
{
    /** @return array<string, mixed> */
    public static function hook(WebProxyEndpoint $hook): array
    {
        $gateway = self::gateway($hook);
        $token = (string) ($gateway['token'] ?? '');
        $hostname = (string) ($gateway['hostname'] ?? $token);

        return [
            'id' => $hook->getKey(),
            'external_id' => $gateway['external_id'] ?? $hook->external_id,
            'name' => $gateway['name'] ?? null,
            'token' => $token,
            'hostname' => $hostname,
            'query_name_template' => '{data}.'.$hostname,
            'endpoint_key' => $hook->endpoint_key,
            'active' => $hook->is_active,
            'metadata' => $gateway['metadata'] ?? (object) [],
            'subscriptions_url' => '/registry/endpoints/'.$hook->endpoint_key.'/subscriptions',
            'events_url' => '/dns/hooks/'.$hook->getKey().'/events',
            'usage_url' => '/dns/hooks/'.$hook->getKey().'/usage',
            'created_at' => $hook->created_at?->toAtomString(),
            'updated_at' => $hook->updated_at?->toAtomString(),
        ];
    }

    /** @return array<string, mixed> */
    public static function event(GatewayEndpointEvent $event): array
    {
        $attributes = $event->attributes ?? [];

        return [
            'id' => $event->getKey(),
            'event_id' => $event->event_id,
            'query' => [
                'name' => $attributes['qname'] ?? null,
                'type' => $attributes['qtype'] ?? null,
                'class' => $attributes['qclass'] ?? null,
                'data' => $attributes['data'] ?? null,
            ],
            'source_address' => $event->source_address,
            'transport' => $event->transport,
            'decision' => [
                'action' => $event->decision_action,
                'status_code' => $event->status_code,
                'error' => $event->error,
            ],
            'payload' => $event->payload ?? (object) [],
            'received_at' => $event->received_at?->toAtomString(),
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
