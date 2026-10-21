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
use Webong\Gateway\Email\AccountReceivingStore;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\EmailVerificationProfile;
use Webong\Gateway\Email\VerificationRecordMissing;
use Webong\Gateway\Email\MxResolver;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class AccountReceivingController
{
    public function __construct(private AccountStore $accounts, private AccountReceivingStore $store,
        private RegistryRequestAuthenticator $registryAuth, private MxResolver $mx,
        private \Webong\Gateway\Email\WorkerIngressStore $workerIngress,
        private EmailVerificationProfile $verificationProfile) {}

    public function settings(Request $request): JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        if (($denied = $this->registryAuth->authorize($request)) !== null) { return $denied; }
        $row = $this->store->settings();
        return $this->ok($row?->document ?? ['mx_host' => null, 'forwarding_domain' => null]);
    }

    public function configure(Request $request): JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        if (($denied = $this->registryAuth->authorize($request)) !== null) { return $denied; }
        try { $row = $this->store->configure($request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid receiving settings.', 422); }
        catch (EmailRouteRejected|VerificationRecordMissing) { return $this->error('Forwarding domain is not verified and active.', 409); }
        catch (RuntimeException) { return $this->error('DNS verification is unavailable.', 503); }
        return $this->ok($row->document);
    }

    public function domains(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->domainResource(...), $this->store->domains($account))]);
    }

    public function createDomain(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_keys($input) !== ['domain'] || ! is_string($input['domain'])) { return $this->error('Invalid receiving domain.', 422); }
        try { $domain = $this->store->createDomain($account, $input['domain']); }
        catch (InvalidArgumentException) { return $this->error('Invalid or already managed receiving domain.', 422); }
        catch (QueryException) { return $this->error('Receiving domain already exists.', 409); }
        return $this->ok($this->domainResource($domain), 201);
    }

    public function showDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        return $row === null ? $this->error('Receiving domain not found.', 404) : $this->ok($this->domainResource($row));
    }

    public function verifyDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Receiving domain not found.', 404); }
        try { return $this->ok($this->domainResource($this->store->verifyDomain($row))); }
        catch (VerificationRecordMissing) { return $this->error('Verification TXT record was not found.', 409); }
        catch (EmailRouteRejected $exception) { return $this->error($exception->getMessage(), 409); }
        catch (QueryException) { return $this->error('Receiving domain is already verified by another account.', 409); }
        catch (RuntimeException) { return $this->error('DNS verification is unavailable.', 503); }
    }

    public function deleteDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Receiving domain not found.', 404); }
        try { $this->store->deleteDomain($row); }
        catch (InvalidArgumentException) { return $this->error('Delete receiving routes first.', 409); }
        return $this->ok(['deleted' => true]);
    }

    public function migrationRecord(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Receiving domain not found.', 404); }
        return $this->ok(['txt' => $this->verificationProfile->proposedRecord(
            $this->store->domainName($row), $row->document['verification_token'])]);
    }

    public function migrateDomain(Request $request, string $account, string $domain): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->domain($account, $domain);
        if ($row === null) { return $this->error('Receiving domain not found.', 404); }
        try { $this->verificationProfile->migrate($account, $this->store->domainName($row)); }
        catch (VerificationRecordMissing) { return $this->error('New verification TXT record was not found.', 409); }
        catch (InvalidArgumentException) { return $this->error('Shared domain verification is inconsistent.', 409); }
        catch (RuntimeException) { return $this->error('DNS verification is unavailable.', 503); }
        return $this->ok($this->domainResource($this->store->domain($account, $domain)));
    }

    public function routes(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->routeResource(...), $this->store->routes($account))]);
    }

    public function createRoute(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['mode', 'domain_id', 'local_part', 'callback_url', 'external_id', 'webhook_secret', 'network_scope']) !== []) {
            return $this->error('Invalid receiving route.', 422);
        }
        try { $created = $this->store->createRoute($account, $input); }
        catch (EmailRouteRejected $exception) { return $this->error($exception->getMessage(), 409); }
        catch (InvalidArgumentException) { return $this->error('Invalid receiving route.', 422); }
        catch (QueryException) { return $this->error('Receiving address already exists.', 409); }
        catch (RuntimeException) { return $this->error('Receiving route verification is unavailable.', 503); }
        return $this->ok([...$this->routeResource($created['record']), 'webhook_secret' => $created['secret']], 201);
    }

    public function showRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        return $row === null ? $this->error('Receiving route not found.', 404) : $this->ok($this->routeResource($row));
    }

    public function updateRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        if ($row === null) { return $this->error('Receiving route not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) === ['mode'] && $input['mode'] === 'worker') {
            try { return $this->ok($this->routeResource($this->store->switchRouteToWorker($row))); }
            catch (EmailRouteRejected $exception) { return $this->error($exception->getMessage(), 409); }
        }
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Only enabled or a switch to Worker mode may change.', 422);
        }
        return $this->ok($this->routeResource($this->store->setRouteEnabled($row, $input['enabled'])));
    }

    public function deleteRoute(Request $request, string $account, string $route): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->store->route($account, $route);
        if ($row === null) { return $this->error('Receiving route not found.', 404); }
        $this->workerIngress->delete($account, $route);
        $this->store->deleteRoute($row);
        return $this->ok(['deleted' => true]);
    }

    private function domainResource(stdClass $row): array
    {
        $name = $this->store->domainName($row);
        $host = $this->store->settings()?->document['mx_host'] ?? null;
        $mxStatus = 'unconfigured';
        if (is_string($host) && $host !== '') {
            try {
                $observed = $this->mx->records($name);
                $mxStatus = count($observed) === 1
                    && $observed[0]['host'] === $host && $observed[0]['priority'] === 10
                    ? 'ready' : 'pending';
            } catch (RuntimeException) { $mxStatus = 'unavailable'; }
        }
        return ['id' => $row->id, 'account_id' => $row->document['account_id'], 'domain' => $name,
            'verified_at' => $row->document['verified_at'], 'enabled' => $row->enabled, 'mx_status' => $mxStatus,
            'records' => [
                'txt' => $this->verificationProfile->record($name,
                    $row->document['verification_token'], $row->document),
                'mx' => ['name' => $name, 'priority' => 10, 'value' => $host],
            ],
        ];
    }

    private function routeResource(stdClass $row): array
    {
        return ['id' => $row->id, 'account_id' => $row->document['account_id'],
            'mode' => $row->document['mode'], 'domain_id' => $row->document['domain_id'],
            'address' => $row->key, 'callback_url' => $row->document['callback_url'],
            'network_scope' => $row->document['network_scope'] ?? 'public',
            'enabled' => $row->enabled, 'status' => $row->enabled ? 'active' : 'disabled'];
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

    private function ok(array $body, int $status = 200): JsonResponse
    {
        return response()->json($body, $status)->header('Cache-Control', 'no-store');
    }

    private function error(string $message, int $status): JsonResponse
    {
        return $this->ok(['message' => $message], $status);
    }
}
