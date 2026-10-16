<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use stdClass;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;
use Webong\Gateway\Schedules\ScheduleNotificationConfig;

final readonly class ScheduleNotificationConfigController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ScheduleNotificationConfig $notifications,
    ) {}

    public function show(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->notifications->find();

        return $row === null ? response()->json(['message' => 'Schedule notification not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->notifications->create($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid schedule notification destination or host is not deployed.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Schedule notification already exists.'], 409);
        }

        return response()->json($this->resource($row), 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            $row = $this->notifications->replace($request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid schedule notification destination or host is not deployed.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Schedule notification has changed.'], 412);
        }

        return $row === null ? response()->json(['message' => 'Schedule notification not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            return $this->notifications->delete($revision) ? response()->noContent()
                : response()->json(['message' => 'Schedule notification not found.'], 404);
        } catch (StaleResource) {
            return response()->json(['message' => 'Schedule notification has changed.'], 412);
        }
    }

    /** @return array{id: string, url: string, token_configured: bool, enabled: bool, revision: int} */
    private function resource(stdClass $row): array
    {
        return ['id' => $row->id, 'url' => $row->document['url'],
            'token_configured' => $row->document['token'] !== '',
            'enabled' => $row->enabled, 'revision' => $row->revision];
    }
}
