<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation\Http\Controllers;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Reconciliation\ProvisioningCoordinator;

final readonly class InternalAssignmentController
{
    public function __construct(
        private ValidationFactory $validator,
        private InternalRequestAuthenticator $authenticator,
        private ProvisioningCoordinator $coordinator,
    ) {}

    public function __invoke(Request $request, string $node): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);
        $validator = $this->validator->make(['node_id' => $node, ...$request->json()->all()], [
            'node_id' => ['required', 'string', 'max:255'],
            'drivers' => ['required', 'array', 'min:1'],
            'drivers.*' => ['string', 'in:local,docker,kubernetes'],
        ]);
        if ($validator->fails()) {
            return response()->json(['message' => 'The provisioning node heartbeat is invalid.', 'errors' => $validator->errors()->toArray()], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $assignment = $this->coordinator->assignments($node, $validator->validated()['drivers']);

        return response()->json(['data' => $assignment['data'], 'meta' => ['leader_id' => $assignment['leader_id']]]);
    }
}
