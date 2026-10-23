<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\OutboundTransportStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Operator transport registry; the account submission API has no provider vocabulary. */
final readonly class OutboundTransportController
{
    public function __construct(private RegistryRequestAuthenticator $registry,
        private InternalRequestAuthenticator $internal, private OutboundTransportStore $store) {}

    public function transports(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->resource(...), $this->store->transports())]);
    }

    public function transport(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        $row = $this->store->transport($id);
        return $row === null ? $this->error('Outbound transport not found.', 404) : $this->ok($this->resource($row));
    }

    public function createTransport(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        try { $row = $this->store->createTransport($request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound transport.', 422); }
        catch (QueryException) { return $this->error('Outbound transport already exists.', 409); }
        return $this->ok($this->resource($row), 201);
    }

    public function updateTransport(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        $row = $this->store->transport($id);
        if ($row === null) { return $this->error('Outbound transport not found.', 404); }
        $input = $request->json()->all();
        if (count($input) !== 1) { return $this->error('Invalid transport update.', 422); }
        if (array_key_exists('healthy', $input) && is_bool($input['healthy'])) {
            return $this->ok($this->resource($this->store->setHealth($row, $input['healthy'])));
        }
        if (array_key_exists('enabled', $input) && is_bool($input['enabled'])) {
            return $this->ok($this->resource($this->store->setEnabled($row, $input['enabled'])));
        }
        if (array_key_exists('domain_readiness', $input) && is_array($input['domain_readiness'])
            && is_string($input['domain_readiness']['domain'] ?? null)
            && is_array($input['domain_readiness']['status'] ?? null)
            && count($input['domain_readiness']) === 2) {
            try { return $this->ok($this->resource($this->store->setDomainReadiness($row,
                $input['domain_readiness']['domain'], $input['domain_readiness']['status']))); }
            catch (InvalidArgumentException) { return $this->error('Invalid sender readiness.', 422); }
        }
        return $this->error('Invalid transport update.', 422);
    }

    public function pools(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->poolResource(...), $this->store->pools())]);
    }

    public function createPool(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        try { $row = $this->store->createPool($request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound pool.', 422); }
        catch (QueryException) { return $this->error('Outbound pool already exists.', 409); }
        return $this->ok($this->poolResource($row), 201);
    }

    public function updatePool(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        $row = $this->store->pool($id);
        if ($row === null) { return $this->error('Outbound pool not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Invalid outbound pool update.', 422);
        }
        return $this->ok($this->poolResource($this->store->setPoolEnabled($row, $input['enabled'])));
    }

    public function bindings(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->bindingResource(...), $this->store->bindings())]);
    }

    public function createBinding(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        try { $row = $this->store->bind($request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound binding.', 422); }
        catch (QueryException) { return $this->error('Outbound binding already exists.', 409); }
        return $this->ok($this->bindingResource($row), 201);
    }

    public function updateBinding(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        $row = $this->store->binding($id);
        if ($row === null) { return $this->error('Outbound binding not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Invalid outbound binding update.', 422);
        }
        return $this->ok($this->bindingResource($this->store->setBindingEnabled($row, $input['enabled'])));
    }

    public function rotateCredential(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        $row = $this->store->transport($id);
        if ($row === null) { return $this->error('Outbound transport not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['password'] || ! is_string($input['password'])) {
            return $this->error('Invalid outbound credential.', 422);
        }
        try { $this->store->rotateCredential($row, $input['password']); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound credential.', 422); }
        return $this->ok(['rotated' => true]);
    }

    public function attempts(Request $request): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) { return $denied; }
        return $this->ok(['data' => array_map(static fn (object $row): array => [
            'delivery_id' => $row->key, 'account_id' => $row->document['account_id'],
            'selected_transport_id' => $row->document['selected_transport_id'], 'state' => $row->document['state'],
        ], $this->store->attempts())]);
    }

    public function plan(Request $request): JsonResponse
    {
        $this->internal->authorizeOrHide($request);
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['pool_id', 'account_id', 'sender_domain', 'delivery_id', 'route_id', 'domain_id']) !== []
            || count($input) !== 6 || ! is_string($input['pool_id'] ?? null)
            || ! is_string($input['account_id'] ?? null) || ! is_string($input['sender_domain'] ?? null)
            || ! is_string($input['delivery_id'] ?? null) || ! is_string($input['route_id'] ?? null)
            || ! (is_string($input['domain_id'] ?? null) || ($input['domain_id'] ?? null) === null)) {
            return $this->error('Invalid outbound plan.', 422);
        }
        try { $rows = $this->store->planAttempt($input['pool_id'], $input['account_id'],
            $input['sender_domain'], $input['delivery_id'], $input['route_id'], $input['domain_id'] ?? ''); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound plan.', 422); }
        catch (EmailRouteRejected) { return $this->error('Outbound transport unavailable.', 409); }
        $transports = [];
        foreach ($rows as $row) {
            $password = $this->store->credential($row);
            if ($password === null) { return $this->error('Outbound transport unavailable.', 503); }
            $transports[] = ['id' => $row->id, 'type' => $row->document['type'],
                'address' => $row->document['address'], 'server_name' => $row->document['server_name'],
                'local_name' => $row->document['local_name'], 'tls_mode' => $row->document['tls_mode'],
                'username' => $row->document['username'], 'password' => $password];
        }
        return $this->ok(['transports' => $transports]);
    }

    public function record(Request $request, string $delivery): JsonResponse
    {
        $this->internal->authorizeOrHide($request);
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['transport_id', 'state']) !== [] || count($input) !== 2
            || ! is_string($input['transport_id']) || ! is_string($input['state'])) {
            return $this->error('Invalid outbound outcome.', 422);
        }
        try { $row = $this->store->recordAttempt($delivery, $input['transport_id'], $input['state']); }
        catch (InvalidArgumentException) { return $this->error('Invalid outbound outcome.', 422); }
        return $row === null ? $this->error('Outbound attempt not found.', 404) : $this->ok(['state' => $row->document['state']]);
    }

    public function reserve(Request $request, string $delivery): JsonResponse
    {
        $this->internal->authorizeOrHide($request);
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['transport_id', 'attempt_index']) !== [] || count($input) !== 2
            || ! is_string($input['transport_id']) || ! is_int($input['attempt_index'])) {
            return $this->error('Invalid outbound reservation.', 422);
        }
        return $this->store->reserveCapacity($delivery, $input['attempt_index'], $input['transport_id'])
            ? $this->ok(['reserved' => true]) : $this->error('Outbound transport capacity is unavailable.', 429);
    }

    private function resource(object $row): array
    {
        $doc = $row->document;
        unset($doc['credential_ref']);
        return ['id' => $row->id, 'enabled' => $row->enabled, ...$doc];
    }

    private function poolResource(object $row): array
    {
        return ['id' => $row->id, 'enabled' => $row->enabled, ...$row->document];
    }

    private function bindingResource(object $row): array
    {
        return ['id' => $row->id, 'enabled' => $row->enabled, ...$row->document];
    }

    private function authorize(Request $request): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        return $this->registry->authorize($request);
    }

    private function ok(array $body, int $status = 200): JsonResponse
    {
        return response()->json($body, $status)->header('Cache-Control', 'no-store');
    }

    private function error(string $message, int $status): JsonResponse { return $this->ok(['message' => $message], $status); }
}
