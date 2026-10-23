<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation;

use Illuminate\Http\Request;
use Illuminate\Support\Str;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Servers\Models\DeploymentTarget;

final class InternalRequestAuthenticator
{
    // Target workers cannot use the shared internal credential, or read other targets.
    public function target(Request $request): ?DeploymentTarget
    {
        if (! $request->hasHeader('X-Gateway-Target')) {
            $this->authorizeOrHide($request);

            return null;
        }
        $id = (string) $request->header('X-Gateway-Target');
        abort_unless(Str::isUuid($id), Response::HTTP_NOT_FOUND);
        $target = DeploymentTarget::query()->find($id);
        $token = (string) $request->header('X-Gateway-Internal', '');
        abort_unless($target !== null && $token !== '' && hash_equals($target->worker_token_hash, hash('sha256', $token)), Response::HTTP_NOT_FOUND);

        return $target;
    }

    public function authorizeOrHide(Request $request): void
    {
        $token = (string) config('gateway.internal_token', '');
        $provided = (string) $request->header('X-Gateway-Internal', '');

        if ($token === '' || $provided === '' || ! hash_equals($token, $provided)) {
            abort(Response::HTTP_NOT_FOUND);
        }
    }
}
