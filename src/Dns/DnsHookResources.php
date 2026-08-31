<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Dns\Models\DnsHook;
use Webong\Gateway\Dns\Models\DnsHookEvent;

final class DnsHookResources
{
    /** @return array<string, mixed> */
    public static function hook(DnsHook $hook): array
    {
        $zone = strtolower(trim((string) config('gateway.dns.zone', ''), '.'));
        $hostname = $zone === '' ? (string) $hook->token : $hook->token.'.'.$zone;

        return [
            'id' => $hook->getKey(),
            'external_id' => $hook->external_id,
            'name' => $hook->name,
            'token' => $hook->token,
            'hostname' => $hostname,
            'query_name_template' => '{data}.'.$hostname,
            'endpoint_key' => $hook->endpoint_key,
            'active' => $hook->is_active,
            'metadata' => $hook->metadata ?? (object) [],
            'usage' => [
                'queries' => $hook->query_count,
                'last_query_at' => $hook->last_queried_at?->toAtomString(),
            ],
            'subscriptions_url' => '/registry/endpoints/'.$hook->endpoint_key.'/subscriptions',
            'events_url' => '/dns/hooks/'.$hook->getKey().'/events',
            'usage_url' => '/dns/hooks/'.$hook->getKey().'/usage',
            'created_at' => $hook->created_at?->toAtomString(),
            'updated_at' => $hook->updated_at?->toAtomString(),
        ];
    }

    /** @return array<string, mixed> */
    public static function event(DnsHookEvent $event): array
    {
        return [
            'id' => $event->getKey(),
            'event_id' => $event->event_id,
            'query' => [
                'name' => $event->query_name,
                'type' => $event->query_type,
                'class' => $event->query_class,
                'data' => $event->data,
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
}
