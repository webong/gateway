<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Carbon\CarbonImmutable;
use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsEndpointRegistry;
use Webong\Gateway\Dns\DnsForwarderStore;
use Webong\Gateway\Dns\DnsObservationStore;
use Webong\Gateway\Dns\DnsZoneStore;
use Webong\Gateway\RegistryRequestAuthenticator;

/** One observation interface for every DNS answer mode. */
final readonly class DnsObservationController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsZoneStore $zones,
        private DnsEndpointRegistry $endpoints,
        private DnsForwarderStore $forwarders,
        private DnsObservationStore $observations,
    ) {}

    public function events(Request $request, string $subject, string $resource): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        if (($resolved = $this->resolve($subject, $resource)) === null) {
            return response()->json(['message' => 'DNS observation subject not found.'], Response::HTTP_NOT_FOUND);
        }
        $input = $request->only(['limit', 'type', 'transport']);
        $validator = $this->validator->make($input, [
            'limit' => ['nullable', 'integer', 'min:1', 'max:100'],
            'type' => ['nullable', 'string', 'max:16', 'regex:/^[A-Za-z0-9-]+$/D'],
            'transport' => ['nullable', 'in:udp,tcp'],
        ]);
        if ($validator->fails()) {
            return response()->json([
                'message' => 'The DNS event filters are invalid.',
                'errors' => $validator->errors()->toArray(),
            ], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        return response()->json($this->observations->events(
            $resolved['kind'],
            $resolved['id'],
            (int) ($input['limit'] ?? 50),
            is_string($input['type'] ?? null) ? $input['type'] : null,
            is_string($input['transport'] ?? null) ? $input['transport'] : null,
        ));
    }

    public function usage(Request $request, string $subject, string $resource): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        if (($resolved = $this->resolve($subject, $resource)) === null) {
            return response()->json(['message' => 'DNS observation subject not found.'], Response::HTTP_NOT_FOUND);
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
            'subject' => $resolved,
            ...$this->observations->usage($resolved['kind'], $resolved['id'], $from, $to),
        ]);
    }

    /** @return array{kind: string, id: string}|null */
    private function resolve(string $subject, string $resource): ?array
    {
        return match ($subject) {
            'zones' => ($model = $this->zones->find($resource)) === null
                ? null : ['kind' => 'dns.zone', 'id' => (string) $model->id],
            'endpoints' => ($model = $this->endpoints->find($resource)) === null
                ? null : ['kind' => 'dns.endpoint', 'id' => (string) $model->getKey()],
            'forwarders' => ($model = $this->forwarders->find($resource)) === null
                ? null : ['kind' => 'dns.forwarder', 'id' => (string) $model->id],
            default => null,
        };
    }
}
