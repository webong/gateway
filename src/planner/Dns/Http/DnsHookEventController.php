<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Dns\DnsHookResources;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsHookEventController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DnsHookRegistry $hooks,
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
        $events = GatewayEndpointEvent::query()
            ->where('endpoint_id', $model->getKey())
            ->where('protocol', 'dns')
            ->when(
                is_string($request->query('type')) && $request->query('type') !== '',
                fn ($query) => $query->where('attributes->qtype', strtoupper((string) $request->query('type'))),
            )
            ->when(
                is_string($request->query('transport')) && $request->query('transport') !== '',
                fn ($query) => $query->where('transport', strtolower((string) $request->query('transport'))),
            )
            ->latest('received_at')
            ->latest('id')
            ->cursorPaginate($limit)
            ->through(DnsHookResources::event(...));

        return response()->json($events);
    }
}
