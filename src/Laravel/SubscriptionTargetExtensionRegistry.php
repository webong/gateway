<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Webong\Gateway\Laravel\Contracts\SubscriptionTargetExtension;
use Webong\Proxy\DestinationRecord;

/** Collects installed Planner extensions that own subscription target kinds. */
final class SubscriptionTargetExtensionRegistry
{
    /** @var array<string, SubscriptionTargetExtension> */
    private array $extensions = [];

    private ?string $defaultKind = null;

    public function register(SubscriptionTargetExtension $extension, bool $default = false): void
    {
        $kind = $extension->kind();
        if (preg_match('/\\A[a-z][a-z0-9.-]{0,63}\\z/D', $kind) !== 1) {
            throw new InvalidArgumentException("Invalid subscription target kind [{$kind}].");
        }
        if (isset($this->extensions[$kind])) {
            throw new InvalidArgumentException("Subscription target kind [{$kind}] is already registered.");
        }
        if ($default && $this->defaultKind !== null) {
            throw new InvalidArgumentException("Subscription target default [{$this->defaultKind}] is already registered.");
        }

        $this->extensions[$kind] = $extension;
        if ($default) {
            $this->defaultKind = $kind;
        }
    }

    public function defaultKind(): ?string
    {
        return $this->defaultKind;
    }

    public function forKind(string $kind): SubscriptionTargetExtension
    {
        return $this->extensions[$kind]
            ?? throw new InvalidArgumentException("Subscription target kind [{$kind}] is not installed.");
    }

    public function forDestination(DestinationRecord $destination): SubscriptionTargetExtension
    {
        $kind = $destination->metadata[SubscriptionDefinition::TARGET_KIND_METADATA_KEY] ?? null;
        if (is_string($kind)) {
            return $this->forKind($kind);
        }

        foreach ($this->extensions as $extension) {
            if ($extension->matchesTarget($destination->target)) {
                return $extension;
            }
        }

        throw new InvalidArgumentException('The subscription target handler is not installed.');
    }

    public function kindForDestination(DestinationRecord $destination): string
    {
        return $this->forDestination($destination)->kind();
    }
}
