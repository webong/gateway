<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class EmailUpstreamController
{
    public function __construct(private RegistryRequestAuthenticator $authenticator,
        private EmailUpstreamCatalog $upstreams) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->upstreams->all())]);
    }

    public function show(Request $request, string $upstream): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->upstreams->find($upstream);

        return $row === null ? response()->json(['message' => 'Email upstream not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->upstreams->create($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Email upstream.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Email upstream already exists.'], 409);
        }

        return response()->json($this->resource($row), 201)->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $upstream): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            $row = $this->upstreams->replace($upstream, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Email upstream.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Email upstream has changed.'], 412);
        }

        return $row === null ? response()->json(['message' => 'Email upstream not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $upstream): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            return $this->upstreams->delete($upstream, $revision) ? response()->noContent()
                : response()->json(['message' => 'Email upstream not found.'], 404);
        } catch (StaleResource) {
            return response()->json(['message' => 'Email upstream has changed.'], 412);
        }
    }

    private function resource(object $row): array
    {
        [$protocol, $name] = explode(':', $row->key, 2);

        return ['id' => $row->id, 'protocol' => $protocol, 'name' => $name,
            ...$row->document, 'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
