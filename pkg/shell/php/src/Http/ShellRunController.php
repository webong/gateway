<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class ShellRunController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DeliveryAdminClient $admin,
    ) {}

    public function __invoke(Request $request, string $run): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if (preg_match('/\A[0-9a-f]{32}\z/D', $run) !== 1) {
            return response()->json(['message' => 'Shell run not found.'], Response::HTTP_NOT_FOUND);
        }
        try {
            $found = $this->admin->findLocal('/shell/runs/'.$run);
        } catch (RuntimeException) {
            return $this->unavailable();
        }
        if ($found === null) {
            return response()->json(['message' => 'Shell run not found.'], Response::HTTP_NOT_FOUND);
        }
        $record = $found['response']->json();
        if (! is_array($record) || ($record['id'] ?? null) !== $run
            || ! is_string($record['hook'] ?? null)
            || ! is_string($record['started_at'] ?? null)
            || ! is_int($record['duration_ms'] ?? null)
            || ! is_int($record['exit_code'] ?? null)
            || ! is_bool($record['failed'] ?? null)) {
            return $this->unavailable();
        }
        $resource = [
            'id' => $record['id'],
            'hook' => $record['hook'],
            'started_at' => $record['started_at'],
            'duration_ms' => $record['duration_ms'],
            'exit_code' => $record['exit_code'],
            'failed' => $record['failed'],
            'node' => $found['node'],
        ];
        foreach (['delivery_id', 'stdout', 'stderr', 'error'] as $field) {
            if (isset($record[$field])) {
                if (! is_string($record[$field])) {
                    return $this->unavailable();
                }
                $resource[$field] = $record[$field];
            }
        }
        foreach (['stdout_truncated', 'stderr_truncated'] as $field) {
            if (isset($record[$field])) {
                if (! is_bool($record[$field])) {
                    return $this->unavailable();
                }
                $resource[$field] = $record[$field];
            }
        }

        return response()->json(['data' => $resource])->header('Cache-Control', 'no-store');
    }

    private function unavailable(): JsonResponse
    {
        return response()->json(['message' => 'Shell run history is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
    }
}
