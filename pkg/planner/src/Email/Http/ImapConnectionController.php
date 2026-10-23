<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use stdClass;
use Throwable;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxConnectionTester;
use Webong\Gateway\Email\MailboxPollStore;

/** Account-owned IMAP retrieval with no SMTP grant or upstream credential. */
final readonly class ImapConnectionController
{
    public function __construct(private AccountStore $accounts, private MailboxConnectionStore $store,
        private MailboxConnectionTester $tester, private MailboxPollStore $polls) {}

    public function index(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->ok(['data' => array_map($this->resource(...), array_values(array_filter(
            $this->store->all($account), static fn (stdClass $row): bool => $row->mode === 'imap')))]);
    }

    public function create(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $created = $this->store->createImap($account, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid IMAP connection.', 422); }
        catch (QueryException) { return $this->error('IMAP address already exists for this account.', 409); }
        return $this->ok(['connection' => $this->resource($created['record']),
            'imap_client_password' => $created['imap_password']], 201);
    }

    public function show(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $row = $this->imap($account, $connection);
        return $row === null ? $this->error('IMAP connection not found.', 404) : $this->ok($this->resource($row));
    }

    public function test(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->imap($account, $connection) === null) { return $this->error('IMAP connection not found.', 404); }
        try { $healthy = $this->tester->test($connection); }
        catch (Throwable) { $healthy = false; }
        return $this->ok($this->resource($this->store->markHealth($account, $connection, $healthy)));
    }

    public function status(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->imap($account, $connection) === null) { return $this->error('IMAP connection not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->error('Only enabled may change.', 422);
        }
        try { $row = $this->store->update($account, $connection, $input['enabled']); }
        catch (RuntimeException) { return $this->error('Test IMAP before enabling.', 409); }
        return $this->ok($this->resource($row));
    }

    public function replace(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->imap($account, $connection) === null) { return $this->error('IMAP connection not found.', 404); }
        try { $row = $this->store->replaceImap($account, $connection, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Invalid IMAP connection.', 422); }
        catch (QueryException) { return $this->error('IMAP address already exists for this account.', 409); }
        return $this->ok($this->resource($row));
    }

    public function rotate(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $password = $this->store->rotateIMAPClientPassword($account, $connection);
        return $password === null ? $this->error('IMAP connection not found.', 404)
            : $this->ok(['imap_client_password' => $password]);
    }

    public function delete(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->imap($account, $connection) === null) { return $this->error('IMAP connection not found.', 404); }
        $this->polls->delete($account, $connection);
        $this->store->delete($account, $connection);
        return $this->ok(['deleted' => true]);
    }

    private function imap(string $account, string $id): ?stdClass
    {
        $row = $this->store->find($account, $id);
        return $row !== null && $row->mode === 'imap' ? $row : null;
    }

    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'account_id' => $row->account_id, 'mode' => 'imap',
            'address' => $row->address, 'imap_upstream_id' => $row->imap_upstream_id,
            'imap_username' => $row->imap_username, 'imap_auth' => $row->imap_auth,
            'credential_mode' => $row->credential_mode ?? 'gateway_managed',
            'credential_source_id' => $row->credential_source_id ?? null,
            'enabled' => (bool) $row->enabled, 'health' => $row->health, 'tested_at' => $row->tested_at];
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
