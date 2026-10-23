<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Throwable;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxPollStore;
use Webong\Gateway\Email\MailboxConnectionTester;

/** Server-to-server control plane; a tenant token cannot address another account. */
final readonly class MailboxConnectionController
{
    public function __construct(private MailboxConnectionStore $store, private MailboxPollStore $polls,
        private MailboxConnectionTester $tester, private AccountStore $accounts) {}

    public function index(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }

        return response()->json(['data' => array_map($this->resource(...), array_values(array_filter(
            $this->store->all($account), static fn (stdClass $row): bool => $row->mode === 'mailbox')))])
            ->header('Cache-Control', 'no-store');
    }

    public function create(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $created = $this->store->create($account, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Connection or upstream is invalid.', 422); }
        catch (QueryException) { return $this->error('Mailbox address already exists for this account.', 409); }

        return response()->json(['connection' => $this->resource($created['record']),
            'imap_client_password' => $created['imap_password'],
            'smtp_client_password' => $created['smtp_password']], 201)->header('Cache-Control', 'no-store');
    }

    public function show(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $record = $this->mailbox($account, $connection);

        return $record === null ? $this->error('Connection not found.', 404)
            : response()->json($this->resource($record))->header('Cache-Control', 'no-store');
    }

    public function test(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->mailbox($account, $connection) === null) { return $this->error('Connection not found.', 404); }
        try { $healthy = $this->tester->test($connection); }
        catch (Throwable) { $healthy = false; }
        $record = $this->store->markHealth($account, $connection, $healthy);

        return response()->json($this->resource($record))->header('Cache-Control', 'no-store');
    }

    public function status(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->mailbox($account, $connection) === null) { return $this->error('Connection not found.', 404); }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) { return $this->error('Only enabled may change.', 422); }
        try { $record = $this->store->update($account, $connection, $input['enabled']); }
        catch (RuntimeException) { return $this->error('Test both transports before enabling.', 409); }

        return $record === null ? $this->error('Connection not found.', 404)
            : response()->json($this->resource($record))->header('Cache-Control', 'no-store');
    }

    public function replace(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->mailbox($account, $connection) === null) { return $this->error('Connection not found.', 404); }
        try { $record = $this->store->replace($account, $connection, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Connection or upstream is invalid.', 422); }
        catch (QueryException) { return $this->error('Mailbox address already exists for this account.', 409); }

        return $record === null ? $this->error('Connection not found.', 404)
            : response()->json($this->resource($record))->header('Cache-Control', 'no-store');
    }

    public function delete(Request $request, string $account, string $connection): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        if ($this->mailbox($account, $connection) === null) { return $this->error('Connection not found.', 404); }

        $this->polls->delete($account, $connection);
        return $this->store->delete($account, $connection)
            ? response()->json(['deleted' => true])->header('Cache-Control', 'no-store')
            : $this->error('Connection not found.', 404);
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (! $request->isSecure()) { return $this->error('HTTPS is required.', 403); }
        $token = $request->bearerToken();
        if (! is_string($token) || ! $this->accounts->authorized($account, $token)) { return $this->error('Unauthenticated.', 401); }

        return null;
    }

    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'account_id' => $row->account_id, 'address' => $row->address,
            'imap_upstream_id' => $row->imap_upstream_id, 'smtp_upstream_id' => $row->smtp_upstream_id,
            'imap_username' => $row->imap_username, 'smtp_username' => $row->smtp_username,
            'imap_auth' => $row->imap_auth, 'smtp_auth' => $row->smtp_auth,
            'credential_mode' => $row->credential_mode ?? 'gateway_managed',
            'credential_source_id' => $row->credential_source_id ?? null,
            'enabled' => (bool) $row->enabled, 'health' => $row->health, 'tested_at' => $row->tested_at];
    }

    private function mailbox(string $account, string $connection): ?stdClass
    {
        $row = $this->store->find($account, $connection);
        return $row !== null && $row->mode === 'mailbox' ? $row : null;
    }

    private function error(string $message, int $code): JsonResponse
    {
        return response()->json(['message' => $message], $code)->header('Cache-Control', 'no-store');
    }
}
