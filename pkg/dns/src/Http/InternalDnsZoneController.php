<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Dns\DnsZoneCatalog;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

final readonly class InternalDnsZoneController
{
    public function __construct(
        private InternalRequestAuthenticator $authenticator,
        private DnsZoneCatalog $zones,
    ) {}

    public function __invoke(Request $request): JsonResponse
    {
        $this->authenticator->authorizeOrHide($request);

        return response()->json(['data' => $this->zones->all()]);
    }
}
