<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Schedules\ScheduleStore;

final readonly class InternalScheduleController
{
    public function __construct(
        private InternalRequestAuthenticator $authenticator,
        private ScheduleStore $store,
        private AutomationLanguageRegistry $languages,
    ) {}

    public function claim(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        $data = $request->validate([
            'automation_languages' => ['sometimes', 'array', 'max:32'],
            'automation_languages.*' => ['required', 'string', 'regex:/^[a-z][a-z0-9_-]*$/'],
        ]);
        $available = array_values(array_filter(
            array_unique($data['automation_languages'] ?? []),
            fn (string $language): bool => $this->languages->supports($language, AutomationCapability::SCHEDULE)
                && $this->languages->target($language) === AutomationExecutionTarget::SPINNER,
        ));

        return response()->json(['data' => $this->store->claim(languages: $available)]);
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
