<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Companion\CompanionActionExecutionStore;
use Webong\Gateway\Companion\CompanionActionIdempotencyConflict;
use Webong\Gateway\Companion\CompanionActionIncompatible;
use Webong\Gateway\Companion\CompanionActionNotFound;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

final readonly class InternalCompanionActionController
{
    public function __construct(
        private InternalRequestAuthenticator $authenticator,
        private CompanionActionRegistry $actions,
        private CompanionActionExecutionStore $executions,
    ) {}

    public function index(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        return response()->json([
            'protocol_version' => CompanionActionInvocation::VERSION,
            'actions' => array_map(
                static fn ($capability): array => $capability->toArray(),
                $this->actions->capabilities(),
            ),
        ])->header('Cache-Control', 'no-store');
    }

    public function execute(Request $request, string $action): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);
        if (strlen($request->getContent()) > (int) config('gateway.companion_actions.max_input_bytes', 262144)) {
            return response()->json(['message' => 'Companion action invocation is too large.'], Response::HTTP_REQUEST_ENTITY_TOO_LARGE);
        }

        try {
            $invocation = CompanionActionInvocation::fromArray($request->json()->all());
            if (! hash_equals($action, $invocation->action)) {
                throw new InvalidArgumentException('Companion action path does not match the invocation.');
            }
            $maxRuntime = max(1, (int) config('gateway.companion_actions.max_runtime_seconds', 300));
            if ($invocation->deadline > now('UTC')->addSeconds($maxRuntime)->toDateTimeImmutable()) {
                throw new InvalidArgumentException('Companion action deadline exceeds the runtime ceiling.');
            }
            $result = $this->executions->execute($invocation);
        } catch (CompanionActionNotFound) {
            return response()->json(['message' => 'Companion action not found.'], Response::HTTP_NOT_FOUND);
        } catch (CompanionActionIncompatible $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (CompanionActionIdempotencyConflict $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid companion action invocation.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $status = $result->status === CompanionActionResult::IN_PROGRESS
            ? Response::HTTP_ACCEPTED
            : Response::HTTP_OK;

        return response()->json($result->toArray(), $status)->header('Cache-Control', 'no-store');
    }
}
