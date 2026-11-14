<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Companion\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Laravel\Companion\CompanionActionActivationStore;
use Webong\Gateway\Laravel\Reconciliation\InternalRequestAuthenticator;

/** Private desired state consumed independently by Planner and Spinner hosts. */
final readonly class InternalExtensionDistributionController
{
    private const ENCODING=JSON_THROW_ON_ERROR|JSON_UNESCAPED_SLASHES|JSON_HEX_TAG|JSON_HEX_APOS|JSON_HEX_AMP|JSON_HEX_QUOT;
    public function __construct(
        private InternalRequestAuthenticator $auth,
        private CompanionActionActivationStore $activations,
        private ExtensionRuntimeConfigurationRegistry $configuration,
    ) {}

    public function __invoke(Request $request): JsonResponse
    {
        $this->auth->authorizeOrHide($request);
        $resources = [];
        foreach ($this->activations->all() as $row) {
            $package = $row->document['package'] ?? null;
            if (! $row->enabled || ! is_array($package)
                || ! is_array($package['artifact'] ?? null)) {
                continue;
            }
            $base = $row->document['configuration'] ?? [];
            $projected = $this->configuration->project($row->key, is_array($base) ? $base : []);
            $serialized = $projected === [] ? new \stdClass : $projected;
            $encoded = json_encode($serialized, self::ENCODING);
            $resources[] = [
                'extension' => $row->key,
                'version' => $package['version'],
                'digest' => $package['digest'] ?? null,
                'artifact' => $package['artifact'],
                'configuration' => $serialized,
                'configuration_sha256' => hash('sha256', $encoded),
                'revision' => $row->revision,
            ];
        }

        usort($resources, static fn (array $left, array $right): int => $left['extension'] <=> $right['extension']);

        return response()->json(['data' => $resources],options:self::ENCODING)->header('Cache-Control', 'no-store');
    }
}
