<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Carbon\CarbonImmutable;
use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsHookUsageController
{
    public function __construct(
        private ValidationFactory $validator,
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

        $input = $request->only(['from', 'to']);
        $validator = $this->validator->make($input, [
            'from' => ['nullable', 'date'],
            'to' => ['nullable', 'date', 'after_or_equal:from'],
        ]);
        if ($validator->fails()) {
            return response()->json([
                'message' => 'The DNS usage period is invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $from = isset($input['from']) ? CarbonImmutable::parse((string) $input['from'])->startOfDay() : now()->subDays(29)->startOfDay();
        $to = isset($input['to']) ? CarbonImmutable::parse((string) $input['to'])->endOfDay() : now()->endOfDay();
        $base = GatewayEndpointEvent::query()
            ->where('endpoint_id', $model->getKey())
            ->where('protocol', 'dns')
            ->whereBetween('received_at', [$from, $to]);

        // Attribute JSON syntax differs between PostgreSQL and SQLite. Keep
        // the generic event table portable; an analytics store can later take
        // over aggregation for high-volume histories.
        $events = (clone $base)->get(['attributes', 'transport', 'received_at']);
        $byType = $events
            ->groupBy(static fn (GatewayEndpointEvent $event): string => (string) (($event->attributes ?? [])['qtype'] ?? ''))
            ->map(static fn ($events): int => $events->count())
            ->sortKeys();
        $byTransport = $events
            ->groupBy(static fn (GatewayEndpointEvent $event): string => (string) ($event->transport ?? ''))
            ->map(static fn ($events): int => $events->count())
            ->sortKeys();
        $daily = $events
            ->groupBy(static fn (GatewayEndpointEvent $event): string => $event->received_at->toDateString())
            ->map(static fn ($events, string $date): array => ['date' => $date, 'queries' => $events->count()])
            ->sortKeys()
            ->values();

        return response()->json([
            'hook_id' => $model->getKey(),
            'period' => ['from' => $from->toAtomString(), 'to' => $to->toAtomString()],
            'queries' => $events->count(),
            'by_type' => $byType,
            'by_transport' => $byTransport,
            'daily' => $daily,
        ]);
    }
}
