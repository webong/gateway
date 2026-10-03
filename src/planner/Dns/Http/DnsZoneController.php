<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Dns\DnsZoneCatalog;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsZoneController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DnsZoneCatalog $zones,
    ) {}

    public function __invoke(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        return response()->json(['data' => $this->zones->all()]);
    }
}
