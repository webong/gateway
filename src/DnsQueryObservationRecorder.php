<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Database\ConnectionInterface;
use Illuminate\Support\Str;
use Webong\Gateway\Events\EventRetention;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Protocols\GatewayObservation;

final readonly class DnsQueryObservationRecorder
{
    public function __construct(
        private ConnectionInterface $database,
        private DnsEndpointRegistry $endpoints,
        private DnsZoneStore $zones,
        private DnsForwarderStore $forwarders,
        private EventRetention $retention,
    ) {}

    public function record(GatewayObservation $observation): void
    {
        if ($observation->type !== 'dns.query') {
            return;
        }

        $this->persist($observation);
        $this->retention->maybePrune();
    }

    private function persist(GatewayObservation $observation): void
    {
        $attributes = $observation->attributes;
        $mode = $attributes['mode'] ?? '';
        $endpoint = null;
        $subjectKind = '';
        $subjectId = '';

        if (in_array($mode, ['managed', 'provided'], true)) {
            $endpoint = ($attributes['endpoint'] ?? '') === ''
                ? null
                : $this->endpoints->find($attributes['endpoint']);
            $owner = $endpoint ?? $this->zones->find((string) ($attributes['zone'] ?? ''));
            $subjectKind = $endpoint === null ? 'dns.zone' : 'dns.endpoint';
            $subjectId = (string) ($endpoint?->getKey() ?? $owner?->id ?? '');
        } elseif ($mode === 'forwarded') {
            $forwarder = $this->forwarders->findByName((string) ($attributes['forwarder'] ?? ''));
            $subjectKind = 'dns.forwarder';
            $subjectId = (string) ($forwarder?->id ?? '');
        }

        // Do not accept observations for resources Planner does not own.
        if ($subjectId === '') {
            return;
        }

        $payload = [
            'query' => [
                'name' => $attributes['qname'] ?? null,
                'type' => $attributes['qtype'] ?? null,
                'class' => $attributes['qclass'] ?? null,
            ],
            'response' => [
                'code' => isset($attributes['response_code']) ? (int) $attributes['response_code'] : null,
                'name' => $attributes['response_name'] ?? null,
                'answer_count' => isset($attributes['answer_count']) ? (int) $attributes['answer_count'] : 0,
                'answer_types' => $attributes['answer_types'] ?? '',
            ],
        ];

        $this->database->transaction(function () use ($observation, $attributes, $endpoint, $subjectKind, $subjectId, $payload): void {
            GatewayEndpointEvent::query()->firstOrCreate(
                ['event_id' => $observation->id],
                [
                    'id' => (string) Str::uuid(),
                    'endpoint_id' => $endpoint?->getKey(),
                    'subject_kind' => $subjectKind,
                    'subject_id' => $subjectId,
                    'protocol' => 'dns',
                    'kind' => 'query',
                    'source_address' => ($attributes['source_address'] ?? '') !== '' ? $attributes['source_address'] : null,
                    'transport' => ($attributes['transport'] ?? '') !== '' ? $attributes['transport'] : null,
                    'decision_action' => ($attributes['decision_action'] ?? '') !== '' ? $attributes['decision_action'] : null,
                    'status_code' => isset($attributes['decision_status_code']) ? (int) $attributes['decision_status_code'] : null,
                    'error' => ($attributes['failure'] ?? '') !== '' ? $attributes['failure'] : null,
                    'attributes' => $attributes,
                    'payload' => $payload,
                    'received_at' => $observation->occurredAt,
                ],
            );
        });
    }
}
