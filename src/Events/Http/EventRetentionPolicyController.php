<?php

declare(strict_types=1);

namespace Webong\Gateway\Events\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Events\EventRetentionPolicyStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

/** Typed Planner CMS API for retention of the shared endpoint event ledger. */
final readonly class EventRetentionPolicyController
{
    public function __construct(private RegistryRequestAuthenticator $auth, private EventRetentionPolicyStore $policies) {}

    public function show(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $row = $this->policies->find();
        return $row === null ? response()->json(['retention_days' => $this->policies->retentionDays(), 'revision' => null])
            : response()->json(['id' => $row->id, ...$row->document, 'revision' => $row->revision])
                ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        try { $row = $this->policies->create($request->json()->all()); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid event retention policy.'], 422); }
        catch (QueryException) { return response()->json(['message' => 'Event retention policy already exists.'], 409); }
        return response()->json(['id' => $row->id, ...$row->document, 'revision' => $row->revision], 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try { $row = $this->policies->replace($request->json()->all(), $revision); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid event retention policy.'], 422); }
        catch (StaleResource) { return response()->json(['message' => 'Event retention policy has changed.'], 412); }
        return $row === null ? response()->json(['message' => 'Event retention policy not found.'], 404)
            : response()->json(['id' => $row->id, ...$row->document, 'revision' => $row->revision])
                ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try { return $this->policies->delete($revision) ? response()->noContent()
            : response()->json(['message' => 'Event retention policy not found.'], 404); }
        catch (StaleResource) { return response()->json(['message' => 'Event retention policy has changed.'], 412); }
    }
}
