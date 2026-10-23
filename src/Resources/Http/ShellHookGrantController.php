<?php

declare(strict_types=1);

namespace Webong\Gateway\Resources\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use stdClass;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;
use Webong\Gateway\ShellHookGrantStore;

final readonly class ShellHookGrantController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ShellHookGrantStore $grants,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->grants->all())]);
    }

    public function show(Request $request, string $grant): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->grants->find($grant);

        return $row === null ? response()->json(['message' => 'Shell hook grant not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authorizeMutation($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->grants->create($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Shell hook grant.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Shell hook grant already exists.'], 409);
        }

        return response()->json($this->resource($row), 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $grant): JsonResponse
    {
        if (($denied = $this->authorizeMutation($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            $row = $this->grants->replace($grant, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Shell hook grant.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Shell hook grant already exists.'], 409);
        } catch (StaleResource) {
            return response()->json(['message' => 'Shell hook grant has changed.'], 412);
        }

        return $row === null ? response()->json(['message' => 'Shell hook grant not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $grant): Response
    {
        if (($denied = $this->authorizeMutation($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            return $this->grants->delete($grant, $revision) ? response()->noContent()
                : response()->json(['message' => 'Shell hook grant not found.'], 404);
        } catch (StaleResource) {
            return response()->json(['message' => 'Shell hook grant has changed.'], 412);
        }
    }

    private function authorizeMutation(Request $request): ?JsonResponse
    {
        return $this->authenticator->authorize($request)
            ?? $this->authenticator->authorizeShellMutation($request);
    }

    /** @return array{id: string, endpoint_key: string, subscriber_id: string, hook: string, enabled: bool, revision: int} */
    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'endpoint_key' => $row->endpoint_key,
            'subscriber_id' => $row->subscriber_id, 'hook' => $row->hook,
            'mode' => $row->mode ?? 'local',
            ...(($row->mode ?? 'local') === 'ssh' ? ['host' => $row->host, 'port' => $row->port, 'user' => $row->user,
                'host_public_key' => $row->host_public_key] : []),
            'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
