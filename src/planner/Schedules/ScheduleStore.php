<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Cron\CronExpression;
use DateTimeImmutable;
use DateTimeZone;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use stdClass;

final class ScheduleStore
{
    public function nextRun(string $cron, string $timezone, ?DateTimeImmutable $after = null): string
    {
        $next = (new CronExpression($cron))->getNextRunDate(
            $after ?? new DateTimeImmutable('now', new DateTimeZone('UTC')),
            0,
            false,
            $timezone,
        );

        return $next->setTimezone(new DateTimeZone('UTC'))->format('Y-m-d H:i:s');
    }

    /** @return list<array<string, mixed>> */
    public function claim(int $limit = 5): array
    {
        return DB::transaction(function () use ($limit): array {
            $now = now('UTC');
            $claims = [];
            $expired = DB::table('schedule_runs')
                ->join('schedules', 'schedules.id', '=', 'schedule_runs.schedule_id')
                ->where('schedules.enabled', true)
                ->where('schedule_runs.status', 'claimed')
                ->where('schedule_runs.lease_until', '<=', $now)
                ->orderBy('schedule_runs.lease_until')->limit($limit)
                ->lockForUpdate()->select('schedule_runs.*')->get();

            foreach ($expired as $run) {
                $token = (string) Str::uuid();
                DB::table('schedule_runs')->where('id', $run->id)->update([
                    'claim_token' => $token,
                    'lease_until' => $now->copy()->addMinutes(2),
                    'updated_at' => $now,
                ]);
                $claims[] = $this->claimResource($run, $token);
            }

            $remaining = $limit - count($claims);
            if ($remaining === 0) {
                return $claims;
            }
            $schedules = DB::table('schedules')->where('enabled', true)
                ->where('next_run_at', '<=', $now)->orderBy('next_run_at')
                ->limit($remaining)->lockForUpdate()->get();

            foreach ($schedules as $schedule) {
                $scheduledFor = $schedule->next_run_at;
                // Missed intervals coalesce into one run; no unbounded catch-up burst.
                $next = $this->nextRun($schedule->cron, $schedule->timezone);
                $id = (string) Str::uuid();
                $token = (string) Str::uuid();
                DB::table('schedule_runs')->insert([
                    'id' => $id,
                    'schedule_id' => $schedule->id,
                    'scheduled_for' => $scheduledFor,
                    'status' => 'claimed',
                    'claim_token' => $token,
                    'lease_until' => $now->copy()->addMinutes(2),
                    'language' => $schedule->language,
                    'source' => $schedule->source,
                    'payload' => $schedule->payload,
                    'created_at' => $now,
                    'updated_at' => $now,
                ]);
                DB::table('schedules')->where('id', $schedule->id)->update([
                    'next_run_at' => $next,
                    'updated_at' => $now,
                ]);
                $claims[] = [
                    'id' => $id,
                    'schedule_id' => $schedule->id,
                    'scheduled_for' => $scheduledFor,
                    'claim_token' => $token,
                    'language' => $schedule->language,
                    'source' => $schedule->source,
                    'payload' => json_decode($schedule->payload, true),
                ];
            }

            return $claims;
        });
    }

    public function complete(string $id, string $token, string $status, int $deliveries, ?string $error): bool
    {
        return DB::table('schedule_runs')->where('id', $id)
            ->where('claim_token', $token)->where('status', 'claimed')
            ->where('lease_until', '>', now('UTC'))
            ->update([
                'status' => $status,
                'deliveries_queued' => $deliveries,
                'error' => $error,
                'completed_at' => now('UTC'),
                'updated_at' => now('UTC'),
            ]) === 1;
    }

    /** @return array<string, mixed> */
    private function claimResource(stdClass $run, string $token): array
    {
        return [
            'id' => $run->id,
            'schedule_id' => $run->schedule_id,
            'scheduled_for' => $run->scheduled_for,
            'claim_token' => $token,
            'language' => $run->language,
            'source' => $run->source,
            'payload' => json_decode($run->payload, true),
        ];
    }
}
