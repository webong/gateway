<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\WorkerIngressStore;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

final readonly class InternalWorkerIngressController
{
    public function __construct(private InternalRequestAuthenticator $auth, private WorkerIngressStore $ingress) {}

    public function authorize(Request $request, string $route): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['token', 'mail_from', 'recipient', 'event_id', 'message_sha256']) !== []
            || count($input) !== 5 || count(array_filter($input, 'is_string')) !== 5) {
            return response()->json(['message' => 'Invalid Worker ingress claim.'], 422);
        }
        try { $decision = $this->ingress->authorize($route, $input['token'], $input['mail_from'],
            $input['recipient'], $input['event_id'], $input['message_sha256']); }
        catch (EmailRouteRejected) { return response()->json(['message' => 'Receiving route is unavailable.'], 409); }
        return $decision === null ? response()->json(['message' => 'Worker ingress denied.'], 403)
            : response()->json($decision)->header('Cache-Control', 'no-store');
    }
}
