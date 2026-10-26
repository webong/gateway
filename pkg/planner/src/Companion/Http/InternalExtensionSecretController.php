<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Companion\ExtensionSecretStore;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Resolve one current secret only for its verified Extension process. */
final readonly class InternalExtensionSecretController
{
    public function __construct(private InternalRequestAuthenticator $auth, private ExtensionSecretStore $secrets) {}

    public function __invoke(Request $request, string $identity, string $reference): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        try {
            $secret = $this->secrets->resolve($identity, $reference);
        } catch (InvalidArgumentException) {
            $secret = null;
        }

        return $secret === null
            ? response()->json(['message' => 'Gateway Extension secret unavailable.'], 404)->header('Cache-Control', 'no-store')
            : response()->json($secret)->header('Cache-Control', 'no-store');
    }
}
