<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Closure;
use InvalidArgumentException;

/** Extension-owned projection from CMS configuration to one Spinner snapshot. */
final class ExtensionRuntimeConfigurationRegistry
{
    /** @var array<string, Closure(array<string, mixed>): array<string, mixed>> */
    private array $providers = [];

    /** @param Closure(array<string, mixed>): array<string, mixed> $provider */
    public function register(string $extension, Closure $provider): void
    {
        if (preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $extension) !== 1 || isset($this->providers[$extension])) {
            throw new InvalidArgumentException('Invalid or duplicate Gateway runtime configuration provider.');
        }

        $this->providers[$extension] = $provider;
    }

    /** @param array<string, mixed> $base
     *  @return array<string, mixed>
     */
    public function project(string $extension, array $base): array
    {
        $configuration = isset($this->providers[$extension])
            ? ($this->providers[$extension])($base)
            : $base;
        if ($configuration !== [] && array_is_list($configuration)) {
            throw new InvalidArgumentException('Gateway runtime configuration must be an object.');
        }
        $encoded = json_encode($configuration, JSON_THROW_ON_ERROR);
        if (strlen($encoded) > 65536) {
            throw new InvalidArgumentException('Gateway runtime configuration exceeds 64 KiB.');
        }

        return $configuration;
    }
}
