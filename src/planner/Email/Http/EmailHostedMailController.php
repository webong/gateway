<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailHostedMailCatalog;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class EmailHostedMailController
{
    public function __construct(private RegistryRequestAuthenticator $auth,
        private EmailHostedMailCatalog $catalog) {}

    public function adapters(Request $request): JsonResponse { return $this->list($request, 'adapter'); }
    public function routes(Request $request): JsonResponse { return $this->list($request, 'route'); }
    public function showAdapter(Request $request, string $id): JsonResponse { return $this->show($request, 'adapter', $id); }
    public function showRoute(Request $request, string $id): JsonResponse { return $this->show($request, 'route', $id); }
    public function createAdapter(Request $request): JsonResponse { return $this->create($request, 'adapter'); }
    public function createRoute(Request $request): JsonResponse { return $this->create($request, 'route'); }
    public function replaceAdapter(Request $request, string $id): JsonResponse { return $this->replace($request, 'adapter', $id); }
    public function replaceRoute(Request $request, string $id): JsonResponse { return $this->replace($request, 'route', $id); }
    public function deleteAdapter(Request $request, string $id): Response { return $this->delete($request, 'adapter', $id); }
    public function deleteRoute(Request $request, string $id): Response { return $this->delete($request, 'route', $id); }

    private function list(Request $request, string $kind): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $rows = $kind === 'adapter' ? $this->catalog->allAdapters() : $this->catalog->allRoutes();

        return response()->json(['data' => array_map(fn (object $row): array => $this->resource($row, $kind), $rows)]);
    }

    private function show(Request $request, string $kind, string $id): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $row = $kind === 'adapter' ? $this->catalog->findAdapter($id) : $this->catalog->findRoute($id);

        return $row === null ? response()->json(['message' => 'Hosted mail resource not found.'], 404)
            : response()->json($this->resource($row, $kind))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    private function create(Request $request, string $kind): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        try {
            $row = $kind === 'adapter' ? $this->catalog->createAdapter($request->json()->all())
                : $this->catalog->createRoute($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid hosted mail resource.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Hosted mail resource already exists.'], 409);
        }

        return response()->json($this->resource($row, $kind), 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    private function replace(Request $request, string $kind, string $id): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try {
            $row = $kind === 'adapter' ? $this->catalog->replaceAdapter($id, $request->json()->all(), $revision)
                : $this->catalog->replaceRoute($id, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid hosted mail resource.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Hosted mail resource has changed.'], 412);
        }

        return $row === null ? response()->json(['message' => 'Hosted mail resource not found.'], 404)
            : response()->json($this->resource($row, $kind))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    private function delete(Request $request, string $kind, string $id): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try {
            $deleted = $kind === 'adapter' ? $this->catalog->deleteAdapter($id, $revision)
                : $this->catalog->deleteRoute($id, $revision);
        } catch (StaleResource) {
            return response()->json(['message' => 'Hosted mail resource has changed.'], 412);
        }

        return $deleted ? response()->noContent() : response()->json(['message' => 'Hosted mail resource not found.'], 404);
    }

    private function resource(object $row, string $kind): array
    {
        return $kind === 'adapter'
            ? ['id' => $row->id, 'name' => $row->key, 'url' => $row->document['url'],
                'has_token' => true, 'enabled' => $row->enabled, 'revision' => $row->revision]
            : ['id' => $row->id, 'recipient' => $row->key, ...$row->document,
                'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
