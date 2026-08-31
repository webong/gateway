<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Carbon\CarbonImmutable;
use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Dns\Models\DnsHookEvent;
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
        $base = DnsHookEvent::query()
            ->where('dns_hook_id', $model->getKey())
            ->whereBetween('received_at', [$from, $to]);

        $byType = (clone $base)
            ->selectRaw('query_type, COUNT(*) AS aggregate')
            ->groupBy('query_type')
            ->orderBy('query_type')
            ->pluck('aggregate', 'query_type')
            ->map(static fn (mixed $count): int => (int) $count);
        $byTransport = (clone $base)
            ->selectRaw('transport, COUNT(*) AS aggregate')
            ->groupBy('transport')
            ->orderBy('transport')
            ->pluck('aggregate', 'transport')
            ->map(static fn (mixed $count): int => (int) $count);
        $daily = (clone $base)
            ->selectRaw('DATE(received_at) AS bucket, COUNT(*) AS aggregate')
            ->groupByRaw('DATE(received_at)')
            ->orderBy('bucket')
            ->get()
            ->map(static fn (DnsHookEvent $event): array => [
                'date' => $event->getAttribute('bucket'),
                'queries' => (int) $event->getAttribute('aggregate'),
            ]);

        return response()->json([
            'hook_id' => $model->getKey(),
            'period' => ['from' => $from->toAtomString(), 'to' => $to->toAtomString()],
            'queries' => (clone $base)->count(),
            'by_type' => $byType,
            'by_transport' => $byTransport,
            'daily' => $daily,
        ]);
    }
}
