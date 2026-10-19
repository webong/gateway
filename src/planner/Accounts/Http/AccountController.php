<?php

declare(strict_types=1);

namespace Webong\Gateway\Accounts\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class AccountController
{
    public function __construct(private AccountStore $accounts, private RegistryRequestAuthenticator $operator) {}

    public function create(Request $request): JsonResponse
    {
        if (($denied = $this->operator->authorize($request)) !== null) { return $denied; }
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['id'] || ! is_string($input['id'])) { return $this->error('Account is invalid.', 422); }
        try { $token = $this->accounts->create($input['id']); }
        catch (InvalidArgumentException) { return $this->error('Account is invalid.', 422); }
        catch (QueryException) { return $this->error('Account already exists.', 409); }

        return response()->json(['id' => $input['id'], 'token' => $token], 201)->header('Cache-Control', 'no-store');
    }

    private function error(string $message, int $code): JsonResponse
    {
        return response()->json(['message' => $message], $code)->header('Cache-Control', 'no-store');
    }
}
