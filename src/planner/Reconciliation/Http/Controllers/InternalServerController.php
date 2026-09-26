<?php

declare(strict_types=1);

namespace Webong\Gateway\Reconciliation\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Resources\ServerResources;

final readonly class InternalServerController
{
    public function __construct(private InternalRequestAuthenticator $authenticator)
    {
    }

    public function __invoke(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        return response()->json([
            'data' => Server::query()->orderBy('id')->get()->map(ServerResources::specification(...)),
        ]);
    }
}
