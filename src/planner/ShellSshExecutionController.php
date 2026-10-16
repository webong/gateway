<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

final readonly class ShellSshExecutionController
{
    public function __construct(private InternalRequestAuthenticator $auth,
        private ShellSshExecutionStore $executions) {}

    public function __invoke(Request $request, string $execution): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $resolved = $this->executions->resolve($execution);

        return $resolved === null
            ? response()->json(['message' => 'SSH Shell execution unavailable.'], 404)->header('Cache-Control', 'no-store')
            : response()->json($resolved)->header('Cache-Control', 'no-store');
    }
}
