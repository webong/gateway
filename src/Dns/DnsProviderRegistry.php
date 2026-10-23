<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;

final class DnsProviderRegistry
{
    /** @var array<string, DnsProvider> */
    private array $providers = [];

    public function register(string $name, DnsProvider $provider): void
    {
        $this->providers[strtolower(trim($name))] = $provider;
    }

    public function resolve(string $name): DnsProvider
    {
        $name = strtolower(trim($name));
        if (! isset($this->providers[$name])) {
            throw new InvalidArgumentException("DNS provider [{$name}] is not registered.");
        }

        return $this->providers[$name];
    }

    /** @return list<string> */
    public function names(): array
    {
        return array_keys($this->providers);
    }
}
