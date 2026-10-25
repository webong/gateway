<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Events\Models\GatewayEndpointEvent;

final class DnsObservationResources
{
    /** @return array<string, mixed> */
    public static function event(GatewayEndpointEvent $event): array
    {
        $attributes = $event->attributes ?? [];

        return [
            'id' => $event->getKey(),
            'event_id' => $event->event_id,
            'mode' => $attributes['mode'] ?? null,
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
            ],
            'response' => [
                'code' => isset($attributes['response_code']) ? (int) $attributes['response_code'] : null,
                'name' => $attributes['response_name'] ?? null,
                'authoritative' => ($attributes['authoritative'] ?? null) === null ? null : $attributes['authoritative'] === 'true',
                'truncated' => ($attributes['truncated'] ?? null) === null ? null : $attributes['truncated'] === 'true',
                'answer_count' => isset($attributes['answer_count']) ? (int) $attributes['answer_count'] : null,
                'answer_types' => $attributes['answer_types'] ?? null,
            ],
            'failure' => $event->error,
            'elapsed_ms' => isset($attributes['elapsed_ms']) ? (int) $attributes['elapsed_ms'] : null,
            'payload' => $event->payload ?? (object) [],
            'received_at' => $event->received_at?->toAtomString(),
        ];
    }
}
