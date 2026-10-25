<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use InvalidArgumentException;

final class DnsProvisionerRegistry
{
    /** @var array<string, DnsProvisioner> */
    private array $provisioners = [];

    public function register(string $name, DnsProvisioner $provisioner): void
    {
        $this->provisioners[strtolower(trim($name))] = $provisioner;
    }

    public function resolve(string $name): DnsProvisioner
    {
        $name = strtolower(trim($name));
        if (! isset($this->provisioners[$name])) {
            throw new InvalidArgumentException("DNS provisioner [{$name}] is not registered.");
        }

        return $this->provisioners[$name];
    }

    /** @return list<string> */
    public function names(): array
    {
        return array_keys($this->provisioners);
    }
}
