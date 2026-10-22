<?php

declare(strict_types=1);

namespace Webong\Gateway\Events\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Proxy\Models\WebProxyEndpoint;

final readonly class EndpointEventController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ValidationFactory $validator,
    ) {}

    public function __invoke(Request $request, string $endpointKey): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        $input = $request->query();
        $validation = $this->validator->make($input, [
            'protocol' => ['sometimes', 'in:http,websocket,webstream,dns,smtp'],
            'kind' => ['sometimes', 'string', 'max:32', 'regex:/^[a-z][a-z_]*$/'],
            'limit' => ['sometimes', 'integer', 'min:1', 'max:100'],
            'cursor' => ['sometimes', 'string', 'max:2048'],
        ]);
        if (array_diff(array_keys($input), ['protocol', 'kind', 'limit', 'cursor']) !== [] || $validation->fails()) {
            return response()->json(['message' => 'The event query is invalid.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        $endpoint = WebProxyEndpoint::query()->where('endpoint_key', $endpointKey)->first();
        if ($endpoint === null) {
            return response()->json(['message' => 'Endpoint not found.'], Response::HTTP_NOT_FOUND);
        }

        $events = GatewayEndpointEvent::query()
            ->where('endpoint_id', $endpoint->getKey())
            ->when(isset($input['protocol']), fn ($query) => $query->where('protocol', $input['protocol']))
            ->when(isset($input['kind']), fn ($query) => $query->where('kind', $input['kind']))
            ->latest('received_at')
            ->latest('id')
            ->cursorPaginate((int) ($input['limit'] ?? 50))
            ->through(static fn (GatewayEndpointEvent $event): array => [
                'id' => $event->getKey(),
                'event_id' => $event->event_id,
                'deliveries_url' => '/deliveries?event_id='.rawurlencode((string) $event->event_id),
                'protocol' => $event->protocol,
                'kind' => $event->kind,
                'source_address' => $event->source_address,
                'decision' => [
                    'action' => $event->decision_action,
                    'status_code' => $event->status_code,
                    'error' => $event->error,
                ],
                'attributes' => $event->attributes ?? (object) [],
                'payload' => $event->payload ?? (object) [],
                'received_at' => $event->received_at?->toAtomString(),
            ]);

        return response()->json($events);
    }
}
