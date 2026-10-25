<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsResolverStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class DnsResolverController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DnsResolverStore $resolvers,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->resource(...), $this->resolvers->all())]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $resolver = $this->resolvers->create($request->json()->all());
        } catch (InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'DNS forwarded resolver already exists.'], Response::HTTP_CONFLICT);
        }

        return response()->json($this->resource($resolver), Response::HTTP_CREATED)
            ->header('ETag', ResourcePrecondition::etag($resolver->revision));
    }

    public function show(Request $request, string $resolver): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->resolvers->find($resolver);

        return $row === null
            ? response()->json(['message' => 'DNS forwarded resolver not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $resolver): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $row = $this->resolvers->replace($resolver, $request->json()->all(), $revision);
        } catch (InvalidArgumentException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_UNPROCESSABLE_ENTITY);
        } catch (QueryException) {
            return response()->json(['message' => 'DNS forwarded resolver already exists.'], Response::HTTP_CONFLICT);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS forwarded resolver has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return $row === null
            ? response()->json(['message' => 'DNS forwarded resolver not found.'], Response::HTTP_NOT_FOUND)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $resolver): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            return $this->resolvers->delete($resolver, $revision)
                ? response()->noContent()
                : response()->json(['message' => 'DNS forwarded resolver not found.'], Response::HTTP_NOT_FOUND);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS forwarded resolver has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
    }

    /** @return array<string, mixed> */
    private function resource(object $resolver): array
    {
        return [
            'id' => $resolver->id,
            'name' => $resolver->name,
            'mode' => 'forwarded',
            ...$resolver->document,
            'enabled' => $resolver->enabled,
            'revision' => $resolver->revision,
            'events_url' => '/dns/resolvers/'.$resolver->id.'/events',
            'usage_url' => '/dns/resolvers/'.$resolver->id.'/usage',
        ];
    }
}
