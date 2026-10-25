<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\EmailDeliveryStatusHookExists;
use Webong\Gateway\Email\EmailDeliveryStatusHookStore;
use Webong\Gateway\Resources\StaleResource;

/** Account API for the single signed outbound-delivery status destination. */
final readonly class EmailDeliveryStatusHookController
{
    public function __construct(private AccountStore $accounts, private EmailDeliveryStatusHookStore $hooks) {}

    public function show(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->hooks->find($account);
        return $row === null ? $this->error('Delivery status hook not found.', 404) : $this->ok($this->resource($row));
    }

    public function create(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $row = $this->hooks->create($account, $request->json()->all()); }
        catch (EmailDeliveryStatusHookExists) { return $this->error('Delivery status hook already exists.', 409); }
        catch (QueryException) { return $this->error('Delivery status hook already exists.', 409); }
        catch (InvalidArgumentException) { return $this->error('Delivery status hook is invalid.', 422); }
        return $this->ok($this->resource($row), 201);
    }

    public function update(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Only enabled may change.', 422);
        }
        try { $row = $this->hooks->setEnabled($account, $input['enabled']); }
        catch (StaleResource) { return $this->error('Delivery status hook changed; retry.', 409); }
        return $row === null ? $this->error('Delivery status hook not found.', 404) : $this->ok($this->resource($row));
    }

    public function delete(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->hooks->delete($account)
            ? $this->ok(['deleted' => true])
            : $this->error('Delivery status hook not found.', 404);
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $token = $request->bearerToken();
        return is_string($token) && $this->accounts->authorized($account, $token)
            ? null : $this->error('Unauthenticated.', 401);
    }

    private function resource(object $row): array
    {
        return [
            'id' => $row->id,
            'account_id' => $row->document['account_id'],
            'callback_url' => $row->document['callback_url'],
            'network_scope' => $row->document['network_scope'],
            'enabled' => $row->enabled,
            'secret_configured' => true,
            'revision' => $row->revision,
        ];
    }

    private function ok(array $body, int $status = 200): JsonResponse
    {
        return response()->json($body, $status)->header('Cache-Control', 'no-store');
    }

    private function error(string $message, int $status): JsonResponse
    {
        return $this->ok(['message' => $message], $status);
    }
}
