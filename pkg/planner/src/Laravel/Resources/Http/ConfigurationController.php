<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Resources\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Laravel\RegistryRequestAuthenticator;
use Webong\Gateway\Laravel\Resources\PlannerResourceStore;
use Webong\Gateway\Resources\ConfigurationKindRegistry;

/** Metadata-only CMS discovery and audit; documents stay behind typed APIs. */
final readonly class ConfigurationController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private ConfigurationKindRegistry $kinds,
        private PlannerResourceStore $resources,
    ) {}

    public function kinds(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => $this->kinds->all()]);
    }

    public function revisions(Request $request, string $kind, string $id): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        if (! $this->kinds->contains($kind)) {
            return response()->json(['message' => 'Configuration kind not found.'], Response::HTTP_NOT_FOUND);
        }
        $history = $this->resources->history($kind, $id);
        if ($history === []) {
            return response()->json(['message' => 'Configuration not found.'], Response::HTTP_NOT_FOUND);
        }

        return response()->json(['data' => $history]);
    }
}
