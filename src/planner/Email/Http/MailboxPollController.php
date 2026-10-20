<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\MailboxPollStore;

final readonly class MailboxPollController
{
    public function __construct(private AccountStore $accounts, private MailboxPollStore $polls) {}

    public function show(Request $request, string $account, string $connection): JsonResponse
    {
        if ($denied = $this->authorize($request, $account)) { return $denied; }
        $row = $this->polls->find($account, $connection);
        return $row === null ? $this->error('Mailbox poll not found.', 404) : $this->present($row);
    }

    public function put(Request $request, string $account, string $connection): JsonResponse
    {
        if ($denied = $this->authorize($request, $account)) { return $denied; }
        $existing = $this->polls->find($account, $connection);
        try { $row = $this->polls->configure($account, $connection, $request->json()->all()); }
        catch (InvalidArgumentException $exception) { return $this->error($exception->getMessage(), 422); }
        return $row === null ? $this->error('Mailbox connection not found.', 404)
            : $this->present($row, $existing === null ? 201 : 200);
    }

    public function delete(Request $request, string $account, string $connection): JsonResponse
    {
        if ($denied = $this->authorize($request, $account)) { return $denied; }
        return $this->polls->delete($account, $connection)
            ? response()->json(['deleted' => true])->header('Cache-Control', 'no-store')
            : $this->error('Mailbox poll not found.', 404);
    }

    private function present(object $row, int $status = 200): JsonResponse
    {
        $checkpoint = DB::table('resource_checkpoints')->where('resource_id', $row->id)->first();
        return response()->json(['route_id' => $row->id, 'connection_id' => $row->document['connection_id'],
            'callback_url' => $row->document['callback_url'], 'enabled' => $row->enabled,
            'cursor' => $checkpoint === null ? null : json_decode($checkpoint->cursor, true),
            'revision' => $row->revision], $status)->header('Cache-Control', 'no-store');
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
