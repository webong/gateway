<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use InvalidArgumentException;

/**
 * Process-local catalog populated by Composer-discovered extension manifests.
 *
 * The contract deliberately stores class names only. Extension packages can
 * announce their Planner companion without depending on Planner or Laravel.
 */
final class ExtensionProviderRegistry
{
    /** @var array<class-string, true> */
    private static array $providers = [];

    /** @param class-string $provider */
    public static function register(string $provider): void
    {
        if ($provider === '') {
            throw new InvalidArgumentException('An extension provider class name is required.');
        }

        self::$providers[$provider] = true;
    }

    /** @return list<class-string> */
    public static function providers(): array
    {
        return array_keys(self::$providers);
    }
}
