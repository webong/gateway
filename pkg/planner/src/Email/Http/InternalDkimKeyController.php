<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Email\EmailDkimKeyStore;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Return one current private key only to an authenticated Spinner. */
final readonly class InternalDkimKeyController
{
    public function __construct(private InternalRequestAuthenticator $auth,
        private EmailDkimKeyStore $keys) {}

    public function __invoke(Request $request, string $domain): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $key = $this->keys->active($domain);

        return $key === null ? response()->json(['message' => 'DKIM key unavailable.'], 404)
            : response()->json($key)->header('Cache-Control', 'no-store');
    }

}
