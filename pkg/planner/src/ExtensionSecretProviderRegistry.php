<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Contracts\ExtensionSecretProvider;

/** Registry for Extension-owned secret namespaces served by Planner. */
final class ExtensionSecretProviderRegistry
{
    /** @var array<string, ExtensionSecretProvider> */
    private array $providers = [];

    public function register(ExtensionSecretProvider $provider): void
    {
        $identity = $provider->identity();
        if (preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $identity) !== 1) {
            throw new InvalidArgumentException('Invalid Extension secret provider identity.');
        }
        if (isset($this->providers[$identity])) {
            throw new InvalidArgumentException("Extension secret provider [{$identity}] is already registered.");
        }

        $this->providers[$identity] = $provider;
    }

    /** @return array{reference: string, version: int, value: array<string, mixed>}|null */
    public function resolve(string $identity, string $reference): ?array
    {
        $provider = $this->providers[$identity] ?? null;
        $secret = $provider?->resolve($reference);
        if ($secret === null) {
            return null;
        }
        if (($secret['reference'] ?? null) !== $reference || ! is_int($secret['version'] ?? null)
            || $secret['version'] < 1 || ! is_array($secret['value'] ?? null)) {
            throw new InvalidArgumentException('Extension secret provider returned an invalid secret.');
        }

        return $secret;
    }
}
