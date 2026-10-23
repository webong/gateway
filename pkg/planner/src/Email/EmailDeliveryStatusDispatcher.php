<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Carbon\CarbonImmutable;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;
use Throwable;
use Webong\Gateway\Deliveries\DeliveryAdminClient;

/** Observes terminal Spinner receipts and sends signed at-least-once account callbacks. */
final readonly class EmailDeliveryStatusDispatcher
{
    public function __construct(
        private EmailDeliveryStatusOutbox $outbox,
        private EmailDeliveryStatusHookStore $hooks,
        private DeliveryAdminClient $admin,
    ) {}

    /** @return array{observed: int, sent: int, retried: int, discarded: int} */
    public function dispatch(int $limit = 25): array
    {
        $limit = max(1, min(100, $limit));
        $summary = ['observed' => 0, 'sent' => 0, 'retried' => 0, 'discarded' => 0];
        foreach ($this->outbox->watching($limit) as $watch) {
            try {
                $response = $this->admin->get('/deliveries/'.$watch->delivery_id, [], $watch->node);
                if (! $response->ok() || $response->json('id') !== $watch->delivery_id) {
                    $this->outbox->deferObservation($watch);
                    continue;
                }
                $state = $response->json('state');
                if (! in_array($state, ['delivered', 'failed'], true)) {
                    $this->outbox->deferObservation($watch);
                    continue;
                }
                $attempts = $response->json('attempts');
                if (! is_int($attempts) || $attempts < 0) {
                    $this->outbox->deferObservation($watch);
                    continue;
                }
                $updatedAt = $response->json('updated_at');
                try {
                    $occurredAt = is_string($updatedAt)
                        ? CarbonImmutable::parse($updatedAt, 'UTC')->utc()->format('Y-m-d\TH:i:s\Z')
                        : now('UTC')->format('Y-m-d\TH:i:s\Z');
                } catch (Throwable) {
                    $occurredAt = now('UTC')->format('Y-m-d\TH:i:s\Z');
                }
                $publicState = $state === 'delivered' ? 'delivered' : 'dead';
                $reason = $state === 'delivered' ? 'accepted'
                    : ($response->json('body_assertion') === 'smtp_acceptance_unknown'
                        ? 'acceptance_unknown' : 'delivery_exhausted');
                $this->outbox->record($watch, $publicState, $attempts, $occurredAt, $reason);
                $summary['observed']++;
            } catch (Throwable) {
                $this->outbox->deferObservation($watch);
            }
        }

        foreach ($this->outbox->claim($limit) as $claim) {
            try { $hook = $this->hooks->active($claim->account_id); }
            catch (Throwable) {
                $this->outbox->retry($claim);
                $summary['retried']++;
                continue;
            }
            if ($hook === null || ! hash_equals($claim->hook_id, $hook['id'])) {
                $this->outbox->discard($claim);
                $summary['discarded']++;
                continue;
            }
            $payload = [
                'event' => 'email.delivery.status',
                'event_id' => $claim->event_id,
                'delivery_id' => $claim->delivery_id,
                'state' => $claim->delivery_state,
                'attempts' => (int) $claim->delivery_attempts,
                'occurred_at' => CarbonImmutable::parse($claim->occurred_at, 'UTC')->utc()->format('Y-m-d\TH:i:s\Z'),
                'reason_code' => $claim->reason_code,
            ];
            $body = json_encode($payload, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
            $canonical = "v1\n{$hook['id']}\n{$claim->event_id}\n".hash('sha256', $body);
            $secret = hex2bin($hook['secret']);
            if ($secret === false) {
                $this->outbox->discard($claim);
                $summary['discarded']++;
                continue;
            }
            try {
                $response = Http::acceptJson()->withBody($body, 'application/json')->withoutRedirecting()
                    ->connectTimeout(2)->timeout(5)->withHeaders([
                        'Idempotency-Key' => 'gateway-email-delivery-'.$claim->event_id,
                        'X-Gateway-Hook-Id' => $hook['id'],
                        'X-Gateway-Event-Id' => $claim->event_id,
                        'X-Gateway-Signature' => 'v1='.hash_hmac('sha256', $canonical, $secret),
                    ])->post($hook['callback_url']);
                if ($response->successful()) {
                    $this->outbox->sent($claim);
                    $summary['sent']++;
                } else {
                    $this->outbox->retry($claim);
                    $summary['retried']++;
                }
            } catch (ConnectionException) {
                $this->outbox->retry($claim);
                $summary['retried']++;
            }
        }

        return $summary;
    }
}
