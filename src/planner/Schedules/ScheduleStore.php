<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Cron\CronExpression;
use DateTimeImmutable;
use DateTimeZone;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use RuntimeException;
use stdClass;
use Webong\Gateway\Protocol\ScheduleCommand;

final class ScheduleStore
{
    private const int MAX_SCRIPT_SCHEDULES_PER_ORIGIN = 32;

    public function __construct(private readonly ScheduleNotificationConfig $notifications) {}

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
    /** @param list<string> $languages */
    public function claim(int $limit = 5, array $languages = []): array
    {
        if ($languages === []) {
            return [];
        }

        return DB::transaction(function () use ($limit, $languages): array {
            $now = now('UTC');
            $claims = [];
            $expired = DB::table('schedule_runs')
                ->join('schedules', 'schedules.id', '=', 'schedule_runs.schedule_id')
                ->where(static function ($query): void {
                    $query->where('schedules.enabled', true)->orWhere('schedule_runs.kind', 'manual');
                })
                ->whereIn('schedule_runs.language', $languages)
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
            $pending = DB::table('schedule_runs')
                ->where('kind', 'manual')->where('status', 'pending')
                ->whereIn('language', $languages)
                ->orderBy('created_at')->orderBy('id')->limit($remaining)
                ->lockForUpdate()->get();
            foreach ($pending as $run) {
                $token = (string) Str::uuid();
                DB::table('schedule_runs')->where('id', $run->id)->where('status', 'pending')->update([
                    'status' => 'claimed',
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
                ->whereIn('language', $languages)
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
                    'kind' => 'cron',
                    'occurrence_key' => 'cron:'.$scheduledFor,
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

    public function enqueueManual(string $scheduleId, ?string $idempotencyKey = null): ?stdClass
    {
        return DB::transaction(function () use ($scheduleId, $idempotencyKey): ?stdClass {
            $schedule = DB::table('schedules')->where('id', $scheduleId)->lockForUpdate()->first();
            if (! $schedule) {
                return null;
            }

            $now = now('UTC');
            $key = 'manual:'.($idempotencyKey === null ? (string) Str::uuid() : hash('sha256', $idempotencyKey));
            DB::table('schedule_runs')->insertOrIgnore([
                'id' => (string) Str::uuid(),
                'schedule_id' => $scheduleId,
                'scheduled_for' => $now,
                'kind' => 'manual',
                'occurrence_key' => $key,
                'status' => 'pending',
                'claim_token' => (string) Str::uuid(),
                'lease_until' => $now,
                'language' => $schedule->language,
                'source' => $schedule->source,
                'payload' => $schedule->payload,
                'created_at' => $now,
                'updated_at' => $now,
            ]);

            $run = DB::table('schedule_runs')->where('schedule_id', $scheduleId)
                ->where('occurrence_key', $key)->first();
            if (! $run) {
                throw new RuntimeException('The manual schedule run could not be queued.');
            }

            return $run;
        });
    }

    public function complete(string $id, string $token, string $status, int $deliveries, ?string $error): bool
    {
        return DB::transaction(function () use ($id, $token, $status, $deliveries, $error): bool {
            $now = now('UTC');
            $updated = DB::table('schedule_runs')->where('id', $id)
                ->where('claim_token', $token)->where('status', 'claimed')
                ->where('lease_until', '>', $now)
                ->update([
                'status' => $status,
                'deliveries_queued' => $deliveries,
                'error' => $error,
                'completed_at' => $now,
                'updated_at' => $now,
                ]) === 1;
            if ($updated && $this->notifications->effective() !== null) {
                DB::table('schedule_notifications')->insertOrIgnore([
                    'run_id' => $id,
                    'state' => 'pending',
                    'next_attempt_at' => $now,
                    'created_at' => $now,
                    'updated_at' => $now,
                ]);
            }

            return $updated;
        });
    }

    /** @param list<ScheduleCommand> $schedules */
    public function upsertScriptSchedules(string $origin, array $schedules): void
    {
        $prepared = array_map(function (ScheduleCommand $schedule): array {
            try {
                $nextRun = $schedule->enabled ? $this->nextRun($schedule->cron, $schedule->timezone) : null;
            } catch (\Throwable) {
                throw new InvalidArgumentException("Schedule [{$schedule->key}] has an invalid cron expression or timezone.");
            }

            return [$schedule, $nextRun];
        }, $schedules);

        DB::transaction(function () use ($origin, $prepared): void {
            $existing = DB::table('schedules')->where('origin', $origin)
                ->lockForUpdate()->get()->keyBy('key');
            $newKeys = collect($prepared)
                ->map(static fn (array $item): string => $item[0]->key)
                ->reject(static fn (string $key): bool => $existing->has($key))
                ->unique();
            if ($existing->count() + $newKeys->count() > self::MAX_SCRIPT_SCHEDULES_PER_ORIGIN) {
                throw new InvalidArgumentException('A script origin cannot own more than 32 schedules.');
            }

            $now = now('UTC');
            $rows = [];
            foreach ($prepared as [$schedule, $nextRun]) {
                $row = $existing->get($schedule->key);
                $rows[] = [
                    'id' => $row?->id ?? (string) Str::uuid(),
                    'origin' => $origin,
                    'key' => $schedule->key,
                    'name' => $schedule->name,
                    'cron' => $schedule->cron,
                    'timezone' => $schedule->timezone,
                    'language' => $schedule->language,
                    'source' => $schedule->source,
                    'payload' => json_encode($schedule->payload, JSON_THROW_ON_ERROR),
                    'enabled' => $schedule->enabled,
                    'next_run_at' => $nextRun,
                    'created_at' => $row?->created_at ?? $now,
                    'updated_at' => $now,
                ];
            }
            DB::table('schedules')->upsert(
                $rows,
                ['origin', 'key'],
                ['name', 'cron', 'timezone', 'language', 'source', 'payload', 'enabled', 'next_run_at', 'updated_at'],
            );
        });
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
            'payload' => (object) (json_decode($run->payload, true, 512, JSON_THROW_ON_ERROR) ?? []),
        ];
    }
}
