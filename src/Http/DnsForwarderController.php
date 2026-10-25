<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsForwarderStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class DnsForwarderController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DnsForwarderStore $forwarders,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->forwarders->all())]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $forwarder = $this->forwarders->create($request->json()->all());
        } catch (InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'DNS forwarder already exists.'], Response::HTTP_CONFLICT);
        }

        return response()->json($this->resource($forwarder), Response::HTTP_CREATED)
            ->header('ETag', ResourcePrecondition::etag($forwarder->revision));
    }

    public function show(Request $request, string $forwarder): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->forwarders->find($forwarder);

        return $row === null
            ? response()->json(['message' => 'DNS forwarder not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $forwarder): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $row = $this->forwarders->replace($forwarder, $request->json()->all(), $revision);
        } catch (InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'DNS forwarder already exists.'], Response::HTTP_CONFLICT);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS forwarder has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return $row === null
            ? response()->json(['message' => 'DNS forwarder not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $forwarder): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            return $this->forwarders->delete($forwarder, $revision)
                ? response()->noContent()
                : response()->json(['message' => 'DNS forwarder not found.'], Response::HTTP_NOT_FOUND);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS forwarder has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
    }

    /** @return array<string, mixed> */
    private function resource(object $forwarder): array
    {
        return [
            'id' => $forwarder->id,
            'name' => $forwarder->name,
            'mode' => 'forwarded',
            ...$forwarder->document,
            'enabled' => $forwarder->enabled,
            'revision' => $forwarder->revision,
            'events_url' => '/dns/forwarders/'.$forwarder->id.'/events',
            'usage_url' => '/dns/forwarders/'.$forwarder->id.'/usage',
            'created_at' => $forwarder->created_at,
            'updated_at' => $forwarder->updated_at,
        ];
    }
}
