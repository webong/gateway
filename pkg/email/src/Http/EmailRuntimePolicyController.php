<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class EmailRuntimePolicyController
{
    public function __construct(private RegistryRequestAuthenticator $auth,
        private EmailRuntimePolicyStore $policies) {}

    public function show(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $row = $this->policies->find();

        return $row === null ? response()->json(['message' => 'Email runtime policy not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        try { $row = $this->policies->create($request->json()->all()); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid Email runtime policy.'], 422); }
        catch (QueryException) { return response()->json(['message' => 'Email runtime policy already exists.'], 409); }

        return response()->json($this->resource($row), 201)->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try { $row = $this->policies->replace($request->json()->all(), $revision); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid Email runtime policy.'], 422); }
        catch (StaleResource) { return response()->json(['message' => 'Email runtime policy has changed.'], 412); }

        return $row === null ? response()->json(['message' => 'Email runtime policy not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try { return $this->policies->delete($revision) ? response()->noContent()
            : response()->json(['message' => 'Email runtime policy not found.'], 404); }
        catch (StaleResource) { return response()->json(['message' => 'Email runtime policy has changed.'], 412); }
    }

    /** @return array<string, bool|int|string> */
    private function resource(object $row): array
    {
        return ['id' => $row->id, ...$row->document, 'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
