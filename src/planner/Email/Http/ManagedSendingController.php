<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\ManagedSendingStore;
use Webong\Gateway\Email\VerificationRecordMissing;

/** Account-facing managed submission identities, independent of external SMTP connections. */
final readonly class ManagedSendingController
{
    public function __construct(private AccountStore $accounts, private ManagedSendingStore $store) {}

    public function domains(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->domainResource(...), $this->store->domains($account))]);
    }

    public function createDomain(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_keys($input) !== ['domain'] || ! is_string($input['domain'])) {
            return $this->error('Invalid sending domain.', 422);
        }
        try { $row = $this->store->createDomain($account, $input['domain']); }
        catch (InvalidArgumentException) { return $this->error('Invalid or already managed sending domain.', 422); }
        catch (QueryException) { return $this->error('Sending domain already exists.', 409); }
        return $this->ok($this->domainResource($row), 201);
    }

    public function showDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        return $row === null ? $this->error('Sending domain not found.', 404) : $this->ok($this->domainResource($row));
    }

    public function verifyDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Sending domain not found.', 404); }
        try { return $this->ok($this->domainResource($this->store->verifyDomain($row))); }
        catch (VerificationRecordMissing) { return $this->error('Verification TXT record was not found.', 409); }
        catch (EmailRouteRejected|QueryException) { return $this->error('Sending domain belongs to another account.', 409); }
        catch (RuntimeException) { return $this->error('DNS verification is unavailable.', 503); }
    }

    public function deleteDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Sending domain not found.', 404); }
        try { $this->store->deleteDomain($row); }
        catch (EmailRouteRejected) { return $this->error('Delete sending routes first.', 409); }
        return $this->ok(['deleted' => true]);
    }

    public function routes(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->routeResource(...), $this->store->routes($account))]);
    }

    public function createRoute(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $created = $this->store->createRoute($account, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid managed sending route.', 422); }
        catch (EmailRouteRejected $exception) { return $this->error($exception->getMessage(), 409); }
        catch (QueryException) { return $this->error('Sending address already exists.', 409); }
        return $this->ok([...$this->routeResource($created['record']),
            'smtp_client_username' => $created['record']->id, 'smtp_client_password' => $created['password']], 201);
    }

    public function showRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        return $row === null ? $this->error('Sending route not found.', 404) : $this->ok($this->routeResource($row));
    }

    public function updateRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        if ($row === null) { return $this->error('Sending route not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Only enabled may change.', 422);
        }
        return $this->ok($this->routeResource($this->store->setEnabled($row, $input['enabled'])));
    }

    public function rotate(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        if ($row === null) { return $this->error('Sending route not found.', 404); }
        $rotated = $this->store->rotate($row);
        return $this->ok(['smtp_client_username' => $route, 'smtp_client_password' => $rotated['password']]);
    }

    public function deleteRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        if ($row === null) { return $this->error('Sending route not found.', 404); }
        $this->store->deleteRoute($row);
        return $this->ok(['deleted' => true]);
    }

    private function domainResource(stdClass $row): array
    {
        $name = $row->document['name'];
        return ['id' => $row->id, 'account_id' => $row->document['account_id'], 'domain' => $name,
            'verified_at' => $row->document['verified_at'], 'enabled' => $row->enabled,
            'dns_status' => ['ownership' => $row->document['verified_at'] === null ? 'pending' : 'verified',
                'spf' => 'not_checked', 'dkim' => 'not_checked'],
            'records' => ['txt' => ['name' => '_gateway-verify.'.$name,
                'value' => 'gateway-verification='.$row->document['verification_token']]]];
    }

    private function routeResource(stdClass $row): array
    {
        return ['id' => $row->id, 'account_id' => $row->owner, 'domain_id' => $row->document['domain_id'],
            'address' => $row->key, 'enabled' => $row->enabled, 'status' => $row->enabled ? 'active' : 'disabled'];
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $token = $request->bearerToken();
        return ! is_string($token) || ! $this->accounts->authorized($account, $token)
            ? $this->error('Unauthenticated.', 401) : null;
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
