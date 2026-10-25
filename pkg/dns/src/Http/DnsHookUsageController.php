<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Carbon\CarbonImmutable;
use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsHookRegistry;
use Webong\Gateway\Dns\DnsObservationStore;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DnsHookUsageController
{
    public function __construct(
        private ValidationFactory $validator,
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
            'hook_id' => $model->getKey(),
            ...$this->observations->usage('dns.hook', (string) $model->getKey(), $from, $to),
        ]);
    }
}
