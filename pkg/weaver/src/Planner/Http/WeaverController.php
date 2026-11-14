<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Planner\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\{JsonResponse, Request};
use InvalidArgumentException;
use Webong\Gateway\Laravel\Accounts\AccountStore;
use Webong\Gateway\Laravel\RegistryRequestAuthenticator;
use Webong\Gateway\Laravel\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;
use Webong\Gateway\Weaver\ApprovedOperations;
use Webong\Gateway\Weaver\Planner\{ResourceConflict, WeaverResources};

/** Operator-managed publication/grants; account credentials alone confer no rights. */
final readonly class WeaverController
{
    public function __construct(private RegistryRequestAuthenticator $operator, private AccountStore $accounts, private WeaverResources $resources) {}

    public function operations(Request $request): JsonResponse
    {
        if (($denied = $this->operator->authorize($request)) !== null) { return $denied; }
        return response()->json(['data' => ApprovedOperations::KEYS])->header('Cache-Control', 'no-store');
    }

    public function definitions(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return response()->json(['data' => array_map($this->resource(...), $this->resources->definitions($account))]);
    }

    public function publish(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { return $this->response($this->resources->publish($account, $request->json()->all()), 201); }
        catch (ResourceConflict|QueryException) { return $this->error('Published version already exists; use a new version.', 409); }
        catch (InvalidArgumentException $error) { return $this->error($error->getMessage(), 422); }
    }

    public function grants(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        return response()->json(['data' => array_map($this->resource(...), $this->resources->grants($account))]);
    }

    public function createGrant(Request $request, string $account): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        try { return $this->response($this->resources->createGrant($account, $request->json()->all()), 201); }
        catch (ResourceConflict|QueryException) { return $this->error('Grant already exists for this scope.', 409); }
        catch (InvalidArgumentException $error) { return $this->error($error->getMessage(), 422); }
    }

    public function replaceGrant(Request $request, string $account, string $grant): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return $this->error('A current If-Match revision is required.', 428); }
        try { $row = $this->resources->replaceGrant($account, $grant, $request->json()->all(), $revision); }
        catch (StaleResource) { return $this->error('Grant revision has changed.', 412); }
        catch (QueryException) { return $this->error('Grant scope already exists.', 409); }
        catch (InvalidArgumentException $error) { return $this->error($error->getMessage(), 422); }
        return $row === null ? $this->error('Grant not found.', 404) : $this->response($row);
    }

    public function revokeGrant(Request $request, string $account, string $grant): JsonResponse
    {
        if (($denied = $this->authorize($request, $account)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return $this->error('A current If-Match revision is required.', 428); }
        try { $deleted = $this->resources->revokeGrant($account, $grant, $revision); }
        catch (StaleResource) { return $this->error('Grant revision has changed.', 412); }
        return $deleted ? response()->json(null, 204) : $this->error('Grant not found.', 404);
    }

    private function authorize(Request $request, string $account): ?JsonResponse
    {
        if (($denied = $this->operator->authorize($request)) !== null) { return $denied; }
        return $this->accounts->exists($account) ? null : $this->error('Account not found.', 404);
    }

    private function response(\stdClass $row, int $status = 200): JsonResponse
    {
        return response()->json($this->resource($row), $status)->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    private function resource(\stdClass $row): array
    {
        return ['id' => $row->id, 'account' => $row->owner, 'document' => $row->document, 'revision' => $row->revision];
    }

    private function error(string $message, int $status): JsonResponse
    {
        return response()->json(['message' => $message], $status);
    }
}
