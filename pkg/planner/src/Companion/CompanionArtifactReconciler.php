<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

/** Reconciles only Planner's entrypoint from generic extension desired state. */
final class CompanionArtifactReconciler
{
    private ?string $signature = null;

    public function __construct(
        private readonly CompanionActionActivationStore $activations,
        private readonly ExtensionArtifactInstaller $installer,
        private readonly ExtCompanionPackageCatalog $catalog,
        private readonly RegisteredCompanionActions $actions,
    ) {}

    public function reconcile(): void
    {
        $rows = array_values(array_filter($this->activations->all(), static function (object $row): bool {
            $companion = $row->document['companion'] ?? null;

            return $row->enabled && is_array($companion) && ($companion['mode'] ?? null) === 'dynamic'
                && is_array($companion['artifact'] ?? null);
        }));
        $signature = hash('sha256', json_encode(array_map(static fn (object $row): array => [
            $row->key, $row->revision, $row->document['companion']['artifact']['sha256'],
        ], $rows), JSON_THROW_ON_ERROR));
        if ($signature === $this->signature) {
            return;
        }
        foreach ($rows as $row) {
            $this->installer->install($row);
        }
        $desired = array_fill_keys(array_map(static fn (object $row): string => $row->key, $rows), true);
        $providers = array_values(array_filter(
            $this->catalog->providers(),
            static fn ($provider): bool => isset($desired[$provider->extension()]),
        ));
        $this->actions->replaceDynamicProviders($providers);
        $this->signature = $signature;
    }
}
