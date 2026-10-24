<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

/** Shared warmed-process lifecycle for every action in one package revision. */
final class WarmedCompanionProcessScope
{
    public ?WarmedCompanionProcess $process = null;

    public ?string $policyKey = null;

    public function stop(): void
    {
        $this->process?->stop();
        $this->process = null;
        $this->policyKey = null;
    }

    public function __destruct()
    {
        $this->stop();
    }
}
