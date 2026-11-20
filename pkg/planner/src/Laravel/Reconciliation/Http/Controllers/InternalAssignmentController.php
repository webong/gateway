<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Reconciliation\Http\Controllers;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Laravel\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Laravel\Reconciliation\ProvisioningCoordinator;

final readonly class InternalAssignmentController
{
    public function __construct(
        private ValidationFactory $validator,
        private InternalRequestAuthenticator $authenticator,
        private ProvisioningCoordinator $coordinator,
    ) {}

    public function __invoke(Request $request, string $node): JsonResponse
    {
        $target = $this->authenticator->target($request);
        $validator = $this->validator->make([...$request->json()->all(), 'node_id' => $node], [
            'node_id' => ['required', 'string', 'max:255'],
            'drivers' => ['required', 'array', 'min:1'],
            'drivers.*' => ['string', 'in:local,docker,kubernetes'],
            'execution_protocol' => ['required', 'in:workload/v1'],
            'owner_id' => ['required', 'string', 'regex:/\A[0-9a-f]{32}\z/D'],
            'legacy_drained' => ['sometimes', 'boolean'],
            'session_id' => ['required', 'string', 'regex:/\A[0-9a-f]{32}\z/D'],
        ]);
        if ($validator->fails()) {
            return response()->json(['message' => 'The provisioning node heartbeat is invalid.', 'errors' => $validator->errors()->toArray()], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $drivers = $validator->validated()['drivers'];
        if ($target !== null && $drivers !== [$target->driver]) {
            return response()->json(['message' => 'Worker drivers must match its target.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        $assignment = $this->coordinator->assignments($node, $drivers, $target?->getKey(), $validator->validated()['session_id'], $validator->validated()['owner_id'], $validator->validated()['legacy_drained'] ?? false);

        return response()->json(['data' => $assignment['data'], 'meta' => [
            'leader_id' => $assignment['leader_id'],
            'execution_protocol' => 'workload/v1',
            'owner_id' => $validator->validated()['owner_id'],
            'issued_at_ms' => $assignment['issued_at_ms'],
            'permission_expires_at_ms' => $assignment['permission_expires_at_ms'],
            'session_id' => $validator->validated()['session_id'] ?? 'legacy',
        ]]);
    }

    public function release(Request $request, string $node): Response
    {
        $target = $this->authenticator->target($request);
        $validator = $this->validator->make($request->json()->all(), [
            'cleanup_confirmed' => ['required', 'accepted'],
            'session_id' => ['required', 'string', 'regex:/\A[0-9a-f]{32}\z/D'],
        ]);
        if ($validator->fails()) {
            return response()->json(['message' => 'The worker session is invalid.', 'errors' => $validator->errors()->toArray()], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        abort_unless($this->coordinator->release($node, $validator->validated()['session_id'], $target?->getKey(), $request->header('X-Gateway-Worker-Owner')), Response::HTTP_CONFLICT);

        return response()->noContent();
    }
}
