<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Schedules\ScheduleStore;

final readonly class InternalScheduleController
{
    public function __construct(
        private InternalRequestAuthenticator $authenticator,
        private ScheduleStore $store,
    ) {}

    public function claim(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        return response()->json(['data' => $this->store->claim()]);
    }

    public function complete(Request $request, string $run): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);
        $data = $request->validate([
            'claim_token' => ['required', 'uuid'],
            'status' => ['required', 'in:succeeded,failed'],
            'deliveries_queued' => ['required', 'integer', 'min:0', 'max:32'],
            'error' => ['nullable', 'string', 'max:2000'],
        ]);
        if (! $this->store->complete($run, $data['claim_token'], $data['status'], $data['deliveries_queued'], $data['error'] ?? null)) {
            return response()->json(['message' => 'The run is no longer claimed by this worker.'], 409);
        }

        return response()->json(['status' => $data['status']]);
    }
}
