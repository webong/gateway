<?php

declare(strict_types=1);

namespace Webong\Gateway\Webstream\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use stdClass;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;

final readonly class WebstreamRoutePolicyController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private WebstreamRoutePolicyStore $policies,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->policies->all())]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->policies->create($request->json()->all());

            return response()->json($this->resource($row), Response::HTTP_CREATED)
                ->header('ETag', ResourcePrecondition::etag($row->revision));
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Webstream route policy.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'Webstream route policy already exists.'], Response::HTTP_CONFLICT);
        }
    }

    public function show(Request $request, string $policy): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->policies->find($policy);

        return $row === null ? response()->json(['message' => 'Webstream route policy not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $policy): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $row = $this->policies->replace($policy, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Webstream route policy.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'Webstream route policy already exists.'], Response::HTTP_CONFLICT);
        } catch (StaleResource) {
            return response()->json(['message' => 'Webstream route policy has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return $row === null ? response()->json(['message' => 'Webstream route policy not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $policy): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }

        try {
            return $this->policies->delete($policy, $revision) ? response()->noContent()
                : response()->json(['message' => 'Webstream route policy not found.'], Response::HTTP_NOT_FOUND);
        } catch (StaleResource) {
            return response()->json(['message' => 'Webstream route policy has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
    }

    /** @return array{id: string, host: string, path: string, source: string, upload: bool, retry: bool, enabled: bool, revision: int} */
    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'host' => $row->host, 'path' => $row->path, 'source' => $row->source,
            'upload' => (bool) $row->upload, 'retry' => (bool) $row->retry,
            'enabled' => (bool) $row->enabled, 'revision' => $row->revision];
    }
}
