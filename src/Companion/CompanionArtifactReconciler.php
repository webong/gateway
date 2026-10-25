<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

/** Reconciles only Planner's entrypoint from generic extension desired state. */
final class CompanionArtifactReconciler
{
    private ?string $signature = null;

    public function __construct(
        private readonly ExtensionArtifactReconciler $artifacts,
        private readonly ExtCompanionPackageCatalog $catalog,
        private readonly RegisteredCompanionActions $actions,
    ) {}

    public function reconcile(): void
    {
        $desiredExtensions = $this->artifacts->reconcile();
        $signature = $this->artifacts->signature();
        if ($signature === $this->signature) {
            return;
        }
        $desired = array_fill_keys($desiredExtensions, true);
        $providers = array_values(array_filter(
            $this->catalog->providers(),
            static fn ($provider): bool => isset($desired[$provider->extension()]),
        ));
        $this->actions->replaceDynamicProviders($providers);
        $this->signature = $signature;
    }
}
