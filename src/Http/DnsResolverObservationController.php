<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Carbon\CarbonImmutable;
use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsObservationStore;
use Webong\Gateway\Dns\DnsResolverStore;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsResolverObservationController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsResolverStore $resolvers,
        private DnsObservationStore $observations,
    ) {}

    public function events(Request $request, string $resolver): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        if (($model = $this->resolvers->find($resolver)) === null) {
            return response()->json(['message' => 'DNS forwarded resolver not found.'], Response::HTTP_NOT_FOUND);
        }
        $limit = min(100, max(1, $request->integer('limit', 50)));
        $type = is_string($request->query('type')) ? $request->query('type') : null;
        $transport = is_string($request->query('transport')) ? $request->query('transport') : null;

        return response()->json($this->observations->events('dns.resolver', $model->id, $limit, $type, $transport));
    }

    public function usage(Request $request, string $resolver): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        if (($model = $this->resolvers->find($resolver)) === null) {
            return response()->json(['message' => 'DNS forwarded resolver not found.'], Response::HTTP_NOT_FOUND);
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

        return response()->json([
            'resolver_id' => $model->id,
            ...$this->observations->usage('dns.resolver', $model->id, $from, $to),
        ]);
    }
}
