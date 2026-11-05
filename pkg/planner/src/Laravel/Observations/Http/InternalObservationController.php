<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Observations\Http;

use Illuminate\Http\Request;
use Illuminate\Http\Response;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response as HttpResponse;
use Webong\Gateway\Laravel\Reconciliation\InternalRequestAuthenticator;
use Webong\Gateway\Observations\GatewayObservationRegistry;
use Webong\Gateway\Protocols\GatewayObservation;

final readonly class InternalObservationController
{
    public function __construct(
        private InternalRequestAuthenticator $authenticator,
        private GatewayObservationRegistry $observations,
    ) {}

    public function __invoke(Request $request): Response
    {
        $this->authenticator->authorizeOrHide($request);
        try {
            $this->observations->record(GatewayObservation::fromArray($request->json()->all()));
        } catch (InvalidArgumentException $exception) {
            abort(HttpResponse::HTTP_UNPROCESSABLE_ENTITY, $exception->getMessage());
        }

        return response()->noContent(HttpResponse::HTTP_ACCEPTED);
    }
}
