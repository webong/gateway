<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\WorkerIngressStore;

final readonly class WorkerIngressController
{
    public function __construct(private AccountStore $accounts, private WorkerIngressStore $ingress) {}

    public function show(Request $request, string $account, string $route): JsonResponse
    {
        if ($denied = $this->authorize($request, $account)) { return $denied; }
        $row = $this->ingress->find($account, $route);
        return $row === null ? $this->error('Worker ingress not found.', 404)
            : response()->json(['route_id' => $route, 'enabled' => $row->enabled,
                'revision' => $row->revision])->header('Cache-Control', 'no-store');
    }

    public function rotate(Request $request, string $account, string $route): JsonResponse
    {
        if ($denied = $this->authorize($request, $account)) { return $denied; }
        if ($request->json()->all() !== []) { return $this->error('Worker ingress does not accept properties.', 422); }
        $created = $this->ingress->rotate($account, $route);
        return $created === null ? $this->error('Worker receiving route not found.', 404)
            : response()->json(['route_id' => $route, 'ingress_token' => $created['token']], 201)
                ->header('Cache-Control', 'no-store');
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $token = $request->bearerToken();
        if (! is_string($token) || ! $this->accounts->authorized($account, $token)) {
            return $this->error('Unauthenticated.', 401);
        }
        return null;
    }

    private function error(string $message, int $status): JsonResponse
    {
        return response()->json(['message' => $message], $status)->header('Cache-Control', 'no-store');
    }
}
