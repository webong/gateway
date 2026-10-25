<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Dns\DnsObservationStore;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsHookEventController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DnsHookRegistry $hooks,
        private DnsObservationStore $observations,
    ) {}

    public function __invoke(Request $request, string $hook): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->hooks->find($hook);
        if ($model === null) {
            return response()->json(['message' => 'DNS hook not found.'], Response::HTTP_NOT_FOUND);
        }

        $limit = min(100, max(1, $request->integer('limit', 50)));
        $type = is_string($request->query('type')) ? $request->query('type') : null;
        $transport = is_string($request->query('transport')) ? $request->query('transport') : null;
        $events = $this->observations->events('dns.hook', (string) $model->getKey(), $limit, $type, $transport);

        return response()->json($events);
    }
}
