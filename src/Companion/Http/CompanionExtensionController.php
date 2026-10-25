<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Automation\ExtAutomationPackageCatalog;
use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

/** Planner CMS surface for static or installable Gateway extensions. */
final readonly class CompanionExtensionController
{
    public function __construct(
        private RegistryRequestAuthenticator $auth,
        private CompanionActionActivationStore $activations,
        private CompanionActionRegistry $actions,
        private ExtAutomationPackageCatalog $automations,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) {
            return $denied;
        }

        try {
            $automationExtensions = $this->automations->extensions();
        } catch (RuntimeException) {
            return response()->json(['message' => 'Gateway extension artifacts are unavailable.'], 503);
        }
        $statuses = $this->actions->statuses();
        foreach ($statuses as &$status) {
            $languages = $automationExtensions[$status['extension']] ?? [];
            $status['automations'] = $languages;
            if ($status['enabled'] && $status['actions'] === [] && $languages !== []) {
                $status['state'] = 'ready';
            }
        }
        unset($status);

        return response()->json(['data' => $statuses]);
    }

    public function show(Request $request, string $extension): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) {
            return $denied;
        }
        $row = $this->activations->find($extension);

        return $row === null
            ? response()->json(['message' => 'Gateway extension activation not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) {
            return $denied;
        }
        try {
            $row = $this->activations->create($request->json()->all());
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Gateway extension activation.'], 422);
        } catch (QueryException) {
            return response()->json(['message' => 'Gateway extension activation already exists.'], 409);
        }

        return response()->json($this->resource($row), 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request, string $extension): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            $row = $this->activations->replace($extension, $request->json()->all(), $revision);
        } catch (InvalidArgumentException) {
            return response()->json(['message' => 'Invalid Gateway extension activation.'], 422);
        } catch (StaleResource) {
            return response()->json(['message' => 'Gateway extension activation has changed.'], 412);
        }

        return $row === null
            ? response()->json(['message' => 'Gateway extension activation not found.'], 404)
            : response()->json($this->resource($row))->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function destroy(Request $request, string $extension): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) {
            return $denied;
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], 428);
        }
        try {
            return $this->activations->delete($extension, $revision)
                ? response()->noContent()
                : response()->json(['message' => 'Gateway extension activation not found.'], 404);
        } catch (StaleResource) {
            return response()->json(['message' => 'Gateway extension activation has changed.'], 412);
        }
    }

    /** @return array<string, mixed> */
    private function resource(object $row): array
    {
        $companion = $row->document['companion'];

        return [
            'id' => $row->id,
            'extension' => $row->key,
            'enabled' => $row->enabled,
            'mode' => $companion['mode'],
            'version' => $companion['version'] ?? null,
            'digest' => $companion['digest'] ?? null,
            'artifact' => $companion['artifact'] ?? null,
            'execution' => $companion['execution'] ?? ['mode' => 'isolated'],
            'revision' => $row->revision,
        ];
    }
}
