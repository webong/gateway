<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Email\EmailHostedMailCatalog;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Resolve one current hosted transport only for a private delivery attempt. */
final readonly class InternalHostedMailAdapterController
{
    public function __construct(private InternalRequestAuthenticator $auth,
        private EmailHostedMailCatalog $catalog) {}

    public function __invoke(Request $request, string $name): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $adapter = $this->catalog->adapter($name);

        return $adapter === null ? response()->json(['message' => 'Hosted mail adapter unavailable.'], 404)
            : response()->json($adapter)->header('Cache-Control', 'no-store');
    }
}
