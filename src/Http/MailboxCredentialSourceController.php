<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Resources\StaleResource;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\MailboxCredentialSourceStore;

/** Register account-owned callback origins; bearer credentials are write-only. */
final readonly class MailboxCredentialSourceController
{
    public function __construct(private MailboxCredentialSourceStore $sources, private AccountStore $accounts) {}

    public function index(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return response()->json(['data' => array_map($this->resource(...), $this->sources->all($account))])
            ->header('Cache-Control', 'no-store');
    }

    public function create(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $row = $this->sources->create($account, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Credential source is invalid.', 422); }
        catch (QueryException) { return $this->error('Credential source already exists.', 409); }
        return response()->json($this->resource($row), 201)->header('Cache-Control', 'no-store');
    }

    public function delete(Request $request, string $account, string $source): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return $this->sources->delete($account, $source)
            ? response()->json(['deleted' => true])->header('Cache-Control', 'no-store')
            : $this->error('Credential source not found.', 404);
    }

    public function replace(Request $request, string $account, string $source): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { $row = $this->sources->replace($account, $source, $request->json()->all()); }
        catch (InvalidArgumentException) { return $this->error('Credential source is invalid.', 422); }
        catch (QueryException) { return $this->error('Credential source already exists.', 409); }
        catch (StaleResource) { return $this->error('Credential source changed; retry.', 409); }
        return $row === null ? $this->error('Credential source not found.', 404)
            : response()->json($this->resource($row))->header('Cache-Control', 'no-store');
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
        return array_filter(['id' => $row->id, 'name' => $row->key, 'url' => $row->document['url'],
            'adapter' => $row->document['adapter'] ?? 'http',
            'language' => $row->document['language'] ?? null, 'source' => $row->document['source'] ?? null,
            'enabled' => $row->enabled, 'revision' => $row->revision],
            static fn (mixed $value): bool => $value !== null);
    }

    private function error(string $message, int $status): JsonResponse
    {
        return response()->json(['message' => $message], $status)->header('Cache-Control', 'no-store');
    }
}
