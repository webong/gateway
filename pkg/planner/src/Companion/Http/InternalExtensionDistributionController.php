<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Reconciliation\InternalRequestAuthenticator;

/** Private desired state consumed independently by Planner and Spinner hosts. */
final readonly class InternalExtensionDistributionController
{
    public function __construct(
        private InternalRequestAuthenticator $auth,
        private CompanionActionActivationStore $activations,
    ) {}

    public function __invoke(Request $request): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $resources = [];
        foreach ($this->activations->all() as $row) {
            $companion = $row->document['companion'] ?? null;
            if (! $row->enabled || ! is_array($companion)
                || ($companion['mode'] ?? null) !== 'dynamic'
                || ! is_array($companion['artifact'] ?? null)) {
                continue;
            }
            $resources[] = [
                'extension' => $row->key,
                'version' => $companion['version'],
                'digest' => $companion['digest'] ?? null,
                'artifact' => $companion['artifact'],
                'revision' => $row->revision,
            ];
        }

        usort($resources, static fn (array $left, array $right): int => $left['extension'] <=> $right['extension']);

        return response()->json(['data' => $resources])->header('Cache-Control', 'no-store');
    }
}
