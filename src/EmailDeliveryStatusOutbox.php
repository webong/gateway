<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;

/** Durable receipt observation and callback retry state. */
final readonly class EmailDeliveryStatusOutbox
{
    public function watch(string $account, string $hook, string $delivery, string $node): void
    {
        $now = now('UTC');
        DB::table('email_delivery_status_events')->insertOrIgnore([
            'delivery_id' => $delivery,
            'event_id' => null,
            'account_id' => $account,
            'hook_id' => $hook,
            'node' => $node,
            'state' => 'watching',
            'delivery_state' => null,
            'delivery_attempts' => null,
            'occurred_at' => null,
            'reason_code' => null,
            'callback_attempts' => 0,
            'next_attempt_at' => $now,
            'lease_until' => null,
            'claim_token' => null,
            'notified_at' => null,
            'created_at' => $now,
            'updated_at' => $now,
        ]);
    }

    /** @return list<object> */
    public function watching(int $limit): array
    {
        return DB::table('email_delivery_status_events')->where('state', 'watching')
            ->where('next_attempt_at', '<=', now('UTC'))->orderBy('created_at')->orderBy('delivery_id')
            ->limit($limit)->get()->all();
    }

    public function record(object $watch, string $state, int $attempts, string $occurredAt, string $reason): void
    {
        $event = hash('sha256', "email.delivery.status\n{$watch->delivery_id}\n{$state}\n{$attempts}");
        DB::table('email_delivery_status_events')->where('delivery_id', $watch->delivery_id)
            ->where('state', 'watching')->update([
                'event_id' => $event,
                'state' => 'pending',
                'delivery_state' => $state,
                'delivery_attempts' => $attempts,
                'occurred_at' => $occurredAt,
                'reason_code' => $reason,
                'next_attempt_at' => now('UTC'),
                'updated_at' => now('UTC'),
            ]);
    }

    public function deferObservation(object $watch): void
    {
        DB::table('email_delivery_status_events')->where('delivery_id', $watch->delivery_id)
            ->where('state', 'watching')->update([
                'next_attempt_at' => now('UTC')->addSeconds(30),
                'updated_at' => now('UTC'),
            ]);
    }

    /** @return list<object> */
    public function claim(int $limit): array
    {
        return DB::transaction(static function () use ($limit): array {
            $now = now('UTC');
            $rows = DB::table('email_delivery_status_events')
                ->where(static function ($query) use ($now): void {
                    $query->where(static function ($pending) use ($now): void {
                        $pending->where('state', 'pending')->where('next_attempt_at', '<=', $now);
                    })->orWhere(static function ($sending) use ($now): void {
                        $sending->where('state', 'sending')->where('lease_until', '<=', $now);
                    });
                })->orderBy('next_attempt_at')->orderBy('delivery_id')->limit($limit)->lockForUpdate()->get();
            $claims = [];
            foreach ($rows as $row) {
                $token = (string) Str::uuid();
                DB::table('email_delivery_status_events')->where('delivery_id', $row->delivery_id)->update([
                    'state' => 'sending',
                    'claim_token' => $token,
                    'lease_until' => $now->copy()->addMinutes(2),
                    'updated_at' => $now,
                ]);
                $row->claim_token = $token;
                $claims[] = $row;
            }

            return $claims;
        });
    }

    public function sent(object $claim): void
    {
        DB::table('email_delivery_status_events')->where('delivery_id', $claim->delivery_id)
            ->where('state', 'sending')->where('claim_token', $claim->claim_token)->update([
                'state' => 'sent',
                'claim_token' => null,
                'lease_until' => null,
                'notified_at' => now('UTC'),
                'updated_at' => now('UTC'),
            ]);
    }

    public function discard(object $claim): void
    {
        DB::table('email_delivery_status_events')->where('delivery_id', $claim->delivery_id)
            ->where('state', 'sending')->where('claim_token', $claim->claim_token)->update([
                'state' => 'discarded',
                'claim_token' => null,
                'lease_until' => null,
                'updated_at' => now('UTC'),
            ]);
    }

    public function retry(object $claim): void
    {
        $attempts = (int) $claim->callback_attempts + 1;
        $delay = min(3600, 30 * (2 ** min(7, $attempts - 1)));
        DB::table('email_delivery_status_events')->where('delivery_id', $claim->delivery_id)
            ->where('state', 'sending')->where('claim_token', $claim->claim_token)->update([
                'state' => 'pending',
                'callback_attempts' => $attempts,
                'next_attempt_at' => now('UTC')->addSeconds($delay),
                'claim_token' => null,
                'lease_until' => null,
                'updated_at' => now('UTC'),
            ]);
    }
}
