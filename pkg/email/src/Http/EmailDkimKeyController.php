<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailDkimKeyStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class EmailDkimKeyController
{
    public function __construct(private RegistryRequestAuthenticator $auth,
        private EmailDkimKeyStore $keys) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }

        return response()->json(['data' => array_map($this->resource(...), $this->keys->all())]);
    }

    public function show(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $row = $this->keys->find($id);

        return $row === null ? response()->json(['message' => 'DKIM key not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        try {
            $row = $this->keys->create($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid DKIM signing key.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'DKIM signing domain already exists.'], 409);
        }

        return response()->json($this->resource($row), 201)->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $id): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try {
            $row = $this->keys->replace($id, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid DKIM signing key.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'DKIM key has changed.'], 412);
        }

        return $row === null ? response()->json(['message' => 'DKIM key not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $id): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try {
            return $this->keys->delete($id, $revision) ? response()->noContent()
                : response()->json(['message' => 'DKIM key not found.'], 404);
        } catch (StaleResource) {
            return response()->json(['message' => 'DKIM key has changed.'], 412);
        }
    }

    private function resource(object $row): array
    {
        return ['id' => $row->id, 'domain' => $row->key, 'selector' => $row->document['selector'],
            'has_private_key' => true, 'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
