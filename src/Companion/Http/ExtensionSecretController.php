<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Companion\ExtensionSecretStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

/** Write-only CMS surface for identity-scoped Extension secrets. */
final readonly class ExtensionSecretController
{
    public function __construct(private RegistryRequestAuthenticator $auth, private ExtensionSecretStore $secrets) {}

    public function index(Request $request, string $extension): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) {
            return $denied;
        }
        $rows = $this->secrets->all($extension);

        return $rows === null
            ? response()->json(['message' => 'Gateway extension activation not found.'], 404)
            : response()->json(['data' => $rows])->header('Cache-Control', 'no-store');
    }

    public function put(Request $request, string $extension, string $reference): JsonResponse
    {
        if (($denied = $this->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        if (array_keys($input) !== ['value'] || ! is_array($input['value']) || array_is_list($input['value'])) {
            return response()->json(['message' => 'Invalid Gateway Extension secret.'], 422);
        }
        try {
            $current = $this->secrets->metadata($extension, $reference);
            $expected = ResourcePrecondition::revision($request);
            if ($current !== null && $expected === null) {
                return response()->json(['message' => 'A current If-Match secret version is required.'], 428);
            }
            $row = $this->secrets->put($extension, $reference, $input['value'], $expected);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Gateway Extension secret.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Gateway Extension secret has changed.'], 412);
        }

        return $row === null
            ? response()->json(['message' => 'Gateway extension activation not found.'], 404)
            : response()->json($row, $current === null ? 201 : 200)
                ->header('ETag', ResourcePrecondition::etag($row['version']))
                ->header('Cache-Control', 'no-store');
    }

    public function destroy(Request $request, string $extension, string $reference): Response
    {
        if (($denied = $this->authorize($request)) !== null) {
            return $denied;
        }
        $expected = ResourcePrecondition::revision($request);
        if ($expected === null) {
            return response()->json(['message' => 'A current If-Match secret version is required.'], 428);
        }
        try {
            $deleted = $this->secrets->delete($extension, $reference, $expected);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Gateway Extension secret reference.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Gateway Extension secret has changed.'], 412);
        }

        return $deleted ? response()->noContent() : response()->json(['message' => 'Gateway Extension secret not found.'], 404);
    }

    private function authorize(Request $request): ?JsonResponse
    {
        if (! $request->isSecure()) {
            return response()->json(['message' => 'HTTPS is required.'], 403);
        }

        return $this->auth->authorize($request);
    }
}
