<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use Closure;

/** Offers compatibility reads while an extension migrates a legacy retention policy. */
final class EventRetentionPolicyFallbackRegistry
{
    /** @var list<Closure(): ?int> */
    private array $providers = [];

    /** @param Closure(): ?int $provider */
    public function register(Closure $provider): void
    {
        $this->providers[] = $provider;
    }

    public function retentionDays(): ?int
    {
        foreach ($this->providers as $provider) {
            $days = $provider();
            if ($days !== null) { return $days; }
        }
        return null;
    }
}
