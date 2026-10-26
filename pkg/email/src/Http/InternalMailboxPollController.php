<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Throwable;
use Webong\Gateway\Email\MailboxPollStore;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

final readonly class InternalMailboxPollController
{
    public function __construct(private InternalRequestAuthenticator $auth, private MailboxPollStore $polls) {}

    public function claim(Request $request, string $poll): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $input = $request->validate(['action' => ['sometimes', 'string', 'in:poll,baseline']]);
        try { $claim = $this->polls->claim($poll, $input['action'] ?? 'poll'); }
        catch (Throwable) { return response()->json(['message' => 'Poll credential unavailable.'], 503)->header('Cache-Control', 'no-store'); }
        return $claim === null ? response()->json(['message' => 'Poll is unavailable.'], 409)
            : response()->json($claim)->header('Cache-Control', 'no-store');
    }

    public function ack(Request $request, string $poll): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $input = $request->validate(['lease_token' => ['required', 'uuid'],
            'uid_validity' => ['required', 'integer', 'min:1'], 'uid' => ['required', 'integer', 'min:1']]);
        return $this->polls->acknowledge($poll, $input['lease_token'], $input['uid_validity'], $input['uid'])
            ? response()->json(['acknowledged' => true]) : response()->json(['message' => 'Poll lease is stale.'], 409);
    }

    public function baseline(Request $request, string $poll): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $input = $request->validate(['lease_token' => ['required', 'uuid'],
            'uid_validity' => ['required', 'integer', 'min:1'], 'uid' => ['required', 'integer', 'min:0']]);
        return $this->polls->baseline($poll, $input['lease_token'], $input['uid_validity'], $input['uid'])
            ? response()->json(['baselined' => true]) : response()->json(['message' => 'Poll lease is stale.'], 409);
    }

    public function release(Request $request, string $poll): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $input = $request->validate(['lease_token' => ['required', 'uuid']]);
        return $this->polls->release($poll, $input['lease_token'])
            ? response()->json(['released' => true]) : response()->json(['message' => 'Poll lease is stale.'], 409);
    }
}
