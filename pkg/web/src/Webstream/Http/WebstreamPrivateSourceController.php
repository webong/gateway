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
use Webong\Gateway\Webstream\WebstreamPrivateSourceStore;

final readonly class WebstreamPrivateSourceController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private WebstreamPrivateSourceStore $sources,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->sources->all())]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->sources->create($request->json()->all());

            return response()->json($this->resource($row), Response::HTTP_CREATED)
                ->header('ETag', ResourcePrecondition::etag($row->revision));
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Webstream private source.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'Webstream private source already exists.'], Response::HTTP_CONFLICT);
        }
    }

    public function show(Request $request, string $source): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->sources->find($source);

        return $row === null ? response()->json(['message' => 'Webstream private source not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $source): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $row = $this->sources->replace($source, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Webstream private source.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'Webstream private source already exists.'], Response::HTTP_CONFLICT);
        } catch (StaleResource) {
            return response()->json(['message' => 'Webstream private source has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return $row === null ? response()->json(['message' => 'Webstream private source not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $source): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }

        try {
            return $this->sources->delete($source, $revision) ? response()->noContent()
                : response()->json(['message' => 'Webstream private source not found.'], Response::HTTP_NOT_FOUND);
        } catch (StaleResource) {
            return response()->json(['message' => 'Webstream private source has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
    }

    /** @return array{id: string, origin: string, cidrs: list<string>, enabled: bool, revision: int} */
    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'origin' => $row->origin,
            'cidrs' => $row->cidrs, 'enabled' => (bool) $row->enabled, 'revision' => $row->revision];
    }
}
