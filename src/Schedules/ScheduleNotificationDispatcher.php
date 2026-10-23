<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Str;
use RuntimeException;

/** At-least-once operator alerts for completed schedule runs. */
final readonly class ScheduleNotificationDispatcher
{
    public function __construct(private ScheduleRunOutcome $outcomes,
        private ScheduleNotificationConfig $notifications) {}

    /** @return array{checked: int, sent: int, deferred: int, resolved: int} */
    public function dispatch(int $limit = 25): array
    {
        $summary = ['checked' => 0, 'sent' => 0, 'deferred' => 0, 'resolved' => 0];
        $destination = $this->notifications->effective();
        if ($destination === null) {
            return $summary;
        }
        $url = $destination['url'];
        $this->validateURL($url);
        foreach ($this->claim(max(1, min(100, $limit))) as $claim) {
            $summary['checked']++;
            $run = DB::table('schedule_runs')->where('id', $claim['run_id'])->first();
            if ($run === null) {
                $this->finish($claim, 'resolved', 'missing');
                $summary['resolved']++;
                continue;
            }

            try {
                $outcome = $this->outcomes->inspect($run);
            } catch (RuntimeException) {
                if ($run->status !== 'failed') {
                    $this->defer($claim, 'unknown', false);
                    $summary['deferred']++;
                    continue;
                }
                // Script failure is already known even if the delivery nodes
                // cannot currently answer a status query.
                $outcome = [
                    'run_id' => $run->id,
                    'execution_status' => 'failed',
                    'delivery_status' => 'unknown',
                    'deliveries_queued' => (int) $run->deliveries_queued,
                    'deliveries_observed' => 0,
                    'counts' => ['queued' => 0, 'deferred' => 0, 'failed' => 0, 'delivered' => 0],
                ];
            }

            if ($run->status !== 'failed' && $outcome['delivery_status'] !== 'failed') {
                if (in_array($outcome['delivery_status'], ['pending', 'unknown'], true)) {
                    $this->defer($claim, $outcome['delivery_status'], false);
                    $summary['deferred']++;
                } else {
                    $this->finish($claim, 'resolved', $outcome['delivery_status']);
                    $summary['resolved']++;
                }
                continue;
            }

            $body = [
                'event' => 'schedule.run.failed',
                'notification_id' => $run->id,
                'schedule_id' => $run->schedule_id,
                'run_id' => $run->id,
                'outcome_url' => '/schedules/'.rawurlencode((string) $run->schedule_id).'/runs/'.rawurlencode((string) $run->id).'/outcome',
                'outcome' => $outcome,
            ];
            try {
                $request = Http::acceptJson()->asJson()->withoutRedirecting()
                    ->connectTimeout(2)->timeout(5)
                    ->withHeaders(['Idempotency-Key' => 'gateway-schedule-run-'.$run->id]);
                $token = $destination['token'];
                if ($token !== '') {
                    $request = $request->withToken($token);
                }
                $response = $request->post($url, $body);
                if ($response->successful()) {
                    $this->finish($claim, 'sent', 'failed');
                    $summary['sent']++;
                } else {
                    $this->defer($claim, 'failed', true);
                    $summary['deferred']++;
                }
            } catch (ConnectionException) {
                $this->defer($claim, 'failed', true);
                $summary['deferred']++;
            }
        }

        return $summary;
    }

    private function validateURL(string $url): void
    {
        $parts = parse_url($url);
        if (! is_array($parts) || ! in_array($parts['scheme'] ?? null, ['http', 'https'], true)
            || ! is_string($parts['host'] ?? null) || $parts['host'] === ''
            || isset($parts['user']) || isset($parts['pass']) || isset($parts['fragment'])
            || isset($parts['query'])) {
            throw new RuntimeException('The schedule notification URL must be an HTTP(S) URL without credentials, query, or fragment.');
        }
    }

    /** @return list<array{run_id: string, token: string}> */
    private function claim(int $limit): array
    {
        return DB::transaction(static function () use ($limit): array {
            $now = now('UTC');
            $rows = DB::table('schedule_notifications')
                ->where(static function ($query) use ($now): void {
                    $query->where(static function ($pending) use ($now): void {
                        $pending->where('state', 'pending')->where('next_attempt_at', '<=', $now);
                    })->orWhere(static function ($checking) use ($now): void {
                        $checking->where('state', 'checking')->where('lease_until', '<=', $now);
                    });
                })
                ->orderBy('next_attempt_at')->orderBy('run_id')
                ->limit($limit)->lockForUpdate()->get();
            $claimed = [];
            foreach ($rows as $row) {
                $token = (string) Str::uuid();
                DB::table('schedule_notifications')->where('run_id', $row->run_id)->update([
                    'state' => 'checking',
                    'claim_token' => $token,
                    'lease_until' => $now->copy()->addMinutes(2),
                    'updated_at' => $now,
                ]);
                $claimed[] = ['run_id' => $row->run_id, 'token' => $token];
            }

            return $claimed;
        });
    }

    /** @param array{run_id: string, token: string} $claim */
    private function finish(array $claim, string $state, string $outcome): void
    {
        DB::table('schedule_notifications')->where('run_id', $claim['run_id'])
            ->where('claim_token', $claim['token'])->where('state', 'checking')->update([
                'state' => $state,
                'claim_token' => null,
                'lease_until' => null,
                'last_outcome' => $outcome,
                'notified_at' => $state === 'sent' ? now('UTC') : null,
                'updated_at' => now('UTC'),
            ]);
    }

    /** @param array{run_id: string, token: string} $claim */
    private function defer(array $claim, string $outcome, bool $sendFailed): void
    {
        $row = DB::table('schedule_notifications')->where('run_id', $claim['run_id'])
            ->where('claim_token', $claim['token'])->where('state', 'checking')->first();
        if ($row === null) {
            return;
        }
        $attempts = (int) $row->attempts + ($sendFailed ? 1 : 0);
        $delay = $sendFailed ? min(3600, 30 * (2 ** min(7, $attempts - 1))) : 30;
        $now = now('UTC');
        DB::table('schedule_notifications')->where('run_id', $claim['run_id'])
            ->where('claim_token', $claim['token'])->where('state', 'checking')->update([
                'state' => 'pending',
                'attempts' => $attempts,
                'next_attempt_at' => $now->copy()->addSeconds($delay),
                'lease_until' => null,
                'claim_token' => null,
                'last_outcome' => $outcome,
                'updated_at' => $now,
            ]);
    }
}
