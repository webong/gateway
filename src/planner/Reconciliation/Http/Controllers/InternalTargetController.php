<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Servers\Http\Controllers\TargetController;

final readonly class InternalTargetController
{
    public function __construct(private InternalRequestAuthenticator $authenticator) {}

    public function __invoke(Request $request, string $target): JsonResponse
    {
        $model = $this->authenticator->target($request);
        abort_unless($model !== null && $model->getKey() === $target, 404);

        return response()->json(TargetController::resource($model));
    }
}
