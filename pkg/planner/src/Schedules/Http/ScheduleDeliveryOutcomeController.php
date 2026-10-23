<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Schedules\ScheduleRunOutcome;

/** Read-only delivery outcome for one schedule run; execution status is separate. */
final readonly class ScheduleDeliveryOutcomeController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ScheduleRunOutcome $outcome,
    ) {}

    public function __invoke(Request $request, string $schedule, string $run): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $record = DB::table('schedule_runs')->where('schedule_id', $schedule)->where('id', $run)->first();
        if ($record === null) {
            return response()->json(['message' => 'Schedule run not found.'], Response::HTTP_NOT_FOUND);
        }

        try {
            return response()->json($this->outcome->inspect($record));
        } catch (RuntimeException) {
            return $this->unavailable();
        }
    }

    private function unavailable(): JsonResponse
    {
        return response()->json(['message' => 'Delivery outcome is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
    }
}
