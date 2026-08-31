<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Database\ConnectionInterface;
use Illuminate\Support\Str;
use Psr\Log\LoggerInterface;
use Throwable;
use Webong\Gateway\Dns\Models\DnsHook;
use Webong\Gateway\Dns\Models\DnsHookEvent;
use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

final readonly class DnsHookEventRecorder
{
    public function __construct(
        private ConnectionInterface $database,
        private ?LoggerInterface $logger = null,
    ) {}

    public function record(GatewayEvent $event, ?GatewayDecision $decision = null, ?Throwable $failure = null): void
    {
        if ($event->protocol !== Protocol::DNS) {
            return;
        }

        try {
            $this->persist($event, $decision, $failure);
        } catch (Throwable $exception) {
            // A history write exception must not replace the route decision.
            $this->logger?->warning('Unable to record Gateway DNS hook event.', [
                'event_id' => $event->id,
                'exception' => $exception,
            ]);
        }
    }

    private function persist(GatewayEvent $event, ?GatewayDecision $decision, ?Throwable $failure): void
    {
        $token = strtolower((string) ($event->attributes['endpoint'] ?? ltrim($event->route, '/')));
        $hook = DnsHook::query()->where('token', $token)->first();
        if ($hook === null) {
            return;
        }

        $receivedAt = now();
        $payload = json_decode($event->payload, true);
        $payload = is_array($payload) ? $payload : ['raw' => $event->payload];

        $this->database->transaction(function () use ($hook, $event, $decision, $failure, $receivedAt, $payload): void {
            $record = DnsHookEvent::query()->firstOrCreate(
                ['event_id' => $event->id],
                [
                    'id' => (string) Str::uuid(),
                    'dns_hook_id' => $hook->getKey(),
                    'query_name' => (string) ($event->attributes['qname'] ?? $event->host),
                    'query_type' => (string) ($event->attributes['qtype'] ?? ''),
                    'query_class' => (string) ($event->attributes['qclass'] ?? ''),
                    'data' => ($event->attributes['data'] ?? '') !== '' ? $event->attributes['data'] : null,
                    'source_address' => ($event->attributes['source_addr'] ?? '') !== '' ? $event->attributes['source_addr'] : null,
                    'transport' => ($event->attributes['transport'] ?? '') !== '' ? $event->attributes['transport'] : null,
                    'decision_action' => $decision?->action->value,
                    'status_code' => $decision?->statusCode ?: null,
                    'error' => $failure?->getMessage(),
                    'payload' => $payload,
                    'received_at' => $receivedAt,
                ],
            );

            if ($record->wasRecentlyCreated) {
                DnsHook::query()->whereKey($hook->getKey())->increment('query_count', 1, [
                    'last_queried_at' => $receivedAt,
                ]);
            }
        });
    }
}
