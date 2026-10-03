<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules\Http;

use Closure;
use Cron\CronExpression;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use Symfony\Component\HttpFoundation\Response;
use Throwable;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Schedules\ScheduleStore;

final readonly class ScheduleController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ScheduleStore $store,
        private AutomationLanguageRegistry $languages,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }

        return response()->json(['data' => DB::table('schedules')->orderBy('created_at', 'desc')
            ->get()->map($this->resource(...))]);
    }

    public function store(Request $request): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['name', 'cron', 'timezone', 'language', 'source', 'payload', 'enabled']) !== []) {
            return $this->invalid('Unsupported properties.');
        }
        $data = $request->validate([
            'name' => ['required', 'string', 'max:255'],
            'cron' => ['required', 'string', 'max:100'],
            'timezone' => ['sometimes', 'string', 'timezone'],
            'language' => ['required', $this->enabledLanguage(...)],
            'source' => ['required', 'string', 'max:65535'],
            'payload' => ['sometimes', 'array'],
            'enabled' => ['sometimes', 'boolean'],
        ]);
        if (! CronExpression::isValidExpression($data['cron']) || trim($data['source']) === '') {
            return $this->invalid('A valid cron expression and non-empty source are required.');
        }
        $id = (string) Str::uuid();
        $timezone = $data['timezone'] ?? 'UTC';
        try {
            $nextRun = $this->store->nextRun($data['cron'], $timezone);
        } catch (Throwable) {
            return $this->invalid('The cron expression has no next occurrence.');
        }
        DB::table('schedules')->insert([
            'id' => $id,
            'name' => $data['name'],
            'cron' => $data['cron'],
            'timezone' => $timezone,
            'language' => $data['language'],
            'source' => $data['source'],
            'payload' => json_encode($data['payload'] ?? [], JSON_THROW_ON_ERROR),
            'enabled' => $data['enabled'] ?? true,
            'next_run_at' => $nextRun,
            'created_at' => now('UTC'),
            'updated_at' => now('UTC'),
        ]);

        return response()->json($this->resource(DB::table('schedules')->where('id', $id)->first()), Response::HTTP_CREATED);
    }

    public function show(Request $request, string $schedule): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        $row = DB::table('schedules')->where('id', $schedule)->first();

        return $row ? response()->json($this->resource($row)) : response()->json(['message' => 'Schedule not found.'], 404);
    }

    public function update(Request $request, string $schedule): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        $row = DB::table('schedules')->where('id', $schedule)->first();
        if (! $row) {
            return response()->json(['message' => 'Schedule not found.'], 404);
        }
        $input = $request->json()->all();
        if ($input === [] || array_diff(array_keys($input), ['name', 'cron', 'timezone', 'language', 'source', 'payload', 'enabled']) !== []) {
            return $this->invalid('Provide supported properties.');
        }
        $data = $request->validate([
            'name' => ['sometimes', 'string', 'max:255'],
            'cron' => ['sometimes', 'string', 'max:100'],
            'timezone' => ['sometimes', 'string', 'timezone'],
            'language' => ['sometimes', $this->enabledLanguage(...)],
            'source' => ['sometimes', 'string', 'max:65535'],
            'payload' => ['sometimes', 'array'],
            'enabled' => ['sometimes', 'boolean'],
        ]);
        if ((isset($data['cron']) && ! CronExpression::isValidExpression($data['cron']))
            || (isset($data['source']) && trim($data['source']) === '')) {
            return $this->invalid('A valid cron expression and non-empty source are required.');
        }
        if (isset($data['payload'])) {
            $data['payload'] = json_encode($data['payload'], JSON_THROW_ON_ERROR);
        }
        if (isset($data['cron']) || isset($data['timezone'])) {
            try {
                $data['next_run_at'] = $this->store->nextRun($data['cron'] ?? $row->cron, $data['timezone'] ?? $row->timezone);
            } catch (Throwable) {
                return $this->invalid('The cron expression has no next occurrence.');
            }
        }
        $data['updated_at'] = now('UTC');
        DB::table('schedules')->where('id', $schedule)->update($data);

        return response()->json($this->resource(DB::table('schedules')->where('id', $schedule)->first()));
    }

    public function destroy(Request $request, string $schedule): Response
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if (DB::table('schedules')->where('id', $schedule)->delete() === 0) {
            return response()->json(['message' => 'Schedule not found.'], 404);
        }

        return response()->noContent();
    }

    public function runs(Request $request, string $schedule): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if (! DB::table('schedules')->where('id', $schedule)->exists()) {
            return response()->json(['message' => 'Schedule not found.'], 404);
        }

        return response()->json(['data' => DB::table('schedule_runs')
            ->leftJoin('schedule_notifications', 'schedule_notifications.run_id', '=', 'schedule_runs.id')
            ->where('schedule_runs.schedule_id', $schedule)
            ->orderBy('schedule_runs.scheduled_for', 'desc')->orderBy('schedule_runs.id', 'desc')->limit(100)
            ->select('schedule_runs.*', 'schedule_notifications.state as notification_status',
                'schedule_notifications.attempts as notification_attempts',
                'schedule_notifications.next_attempt_at as notification_next_attempt_at')->get()
            ->map($this->runResource(...))]);
    }

    public function run(Request $request, string $schedule): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if ($request->all() !== []) {
            return $this->invalid('Manual runs do not accept a request body.');
        }
        $key = $request->header('Idempotency-Key');
        if ($key !== null && (! is_string($key) || preg_match('/\A[A-Za-z0-9._:-]{1,128}\z/D', $key) !== 1)) {
            return $this->invalid('Idempotency-Key must be 1–128 safe ASCII characters.');
        }

        $run = $this->store->enqueueManual($schedule, $key);
        if ($run === null) {
            return response()->json(['message' => 'Schedule not found.'], 404);
        }

        return response()->json($this->runResource($run), Response::HTTP_ACCEPTED);
    }

    /** @return array<string, mixed> */
    private function runResource(object $run): array
    {
        return [
            'id' => $run->id,
            'kind' => $run->kind,
            'scheduled_for' => $run->scheduled_for,
            'status' => $run->status,
            'deliveries_queued' => $run->deliveries_queued,
            'deliveries_url' => '/deliveries?event_id='.rawurlencode((string) $run->id),
            'outcome_url' => '/schedules/'.rawurlencode((string) $run->schedule_id).'/runs/'.rawurlencode((string) $run->id).'/outcome',
            'notification_status' => $run->notification_status ?? null,
            'notification_attempts' => $run->notification_attempts ?? null,
            'notification_next_attempt_at' => $run->notification_next_attempt_at ?? null,
            'error' => $run->error,
            'completed_at' => $run->completed_at,
        ];
    }

    private function resource(object $row): array
    {
        return [
            'id' => $row->id,
            'origin' => $row->origin ?? null,
            'key' => $row->key ?? null,
            'name' => $row->name,
            'cron' => $row->cron,
            'timezone' => $row->timezone,
            'language' => $row->language,
            'source' => $row->source,
            'payload' => json_decode($row->payload, true),
            'enabled' => (bool) $row->enabled,
            'next_run_at' => $row->next_run_at,
            'created_at' => $row->created_at,
            'updated_at' => $row->updated_at,
        ];
    }

    private function enabledLanguage(string $attribute, mixed $value, Closure $fail): void
    {
        if (! is_string($value)
            || ! $this->languages->supports($value, AutomationCapability::SCHEDULE)) {
            $fail("The {$attribute} is not enabled for scheduled automation.");
        }
    }

    private function invalid(string $message): JsonResponse
    {
        return response()->json(['message' => $message], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
