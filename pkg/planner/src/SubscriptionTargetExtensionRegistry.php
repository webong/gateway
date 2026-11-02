<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Contracts\SubscriptionTargetExtension;
use Webong\Proxy\DestinationRecord;

/** Collects optional destination handlers registered by Planner extensions. */
final class SubscriptionTargetExtensionRegistry
{
    /** @var array<string, SubscriptionTargetExtension> */
    private array $extensions = [];

    public function register(SubscriptionTargetExtension $extension): void
    {
        $kind = $extension->kind();
        if (preg_match('/\A[a-z][a-z0-9.-]{0,63}\z/D', $kind) !== 1 || $kind === 'http') {
            throw new InvalidArgumentException("Invalid subscription target kind [{$kind}].");
        }
        if (isset($this->extensions[$kind])) {
            throw new InvalidArgumentException("Subscription target kind [{$kind}] is already registered.");
        }

        $this->extensions[$kind] = $extension;
    }

    public function forKind(string $kind): ?SubscriptionTargetExtension
    {
        if ($kind === 'http') {
            return null;
        }

        return $this->extensions[$kind]
            ?? throw new InvalidArgumentException("Subscription target kind [{$kind}] is not installed.");
    }

    public function forDestination(DestinationRecord $destination): ?SubscriptionTargetExtension
    {
        $kind = $destination->metadata['_gateway_target_kind'] ?? null;
        if (is_string($kind)) {
            return $this->forKind($kind);
        }

        foreach ($this->extensions as $extension) {
            if ($extension->matchesTarget($destination->target)) {
                return $extension;
            }
        }

        $parsed = parse_url($destination->target);
        if (is_array($parsed) && in_array($parsed['scheme'] ?? null, ['http', 'https'], true)
            && is_string($parsed['host'] ?? null) && $parsed['host'] !== '') {
            return null;
        }

        throw new InvalidArgumentException('The subscription target handler is not installed.');
    }

    public function kindForDestination(DestinationRecord $destination): string
    {
        return $this->forDestination($destination)?->kind() ?? 'http';
    }
}
