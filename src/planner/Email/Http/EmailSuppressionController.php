<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailSuppressionStore;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class EmailSuppressionController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private EmailSuppressionStore $suppressions,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $cursor = $request->query('cursor');
        $limit = $request->query('limit', '100');
        if (($cursor !== null && ! is_string($cursor)) || ! is_string($limit) || ! ctype_digit($limit)) {
            return response()->json(['message' => 'Invalid Email suppression page.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        try {
            $pageSize = (int) $limit;
            $rows = $this->suppressions->entries($cursor, $pageSize);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Email suppression page.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        return response()->json([
            'data' => array_slice($rows, 0, $pageSize),
            'next_cursor' => count($rows) > $pageSize ? $rows[$pageSize - 1]->id : null,
        ]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['address', 'reason']) !== []
            || ! is_string($input['address'] ?? null) || ! is_string($input['reason'] ?? null)) {
            return response()->json(['message' => 'A valid address and reason are required.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }
        try {
            $entry = $this->suppressions->suppress($input['address'], $input['reason']);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'A valid address and reason are required.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        return response()->json($entry, Response::HTTP_CREATED);
    }

    public function destroy(Request $request, string $suppression): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return $this->suppressions->remove($suppression)
            ? response()->noContent()
            : response()->json(['message' => 'Email suppression not found.'], Response::HTTP_NOT_FOUND);
    }
}
