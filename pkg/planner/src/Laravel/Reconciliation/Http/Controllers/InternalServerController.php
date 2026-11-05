<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Reconciliation\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Laravel\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Laravel\Servers\Models\Server;
use Webong\Gateway\Laravel\Servers\Resources\ServerResources;

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
