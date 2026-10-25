<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

interface DnsProvisioner
{
    /**
     * @param array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string} $zone
     * @param array<string, mixed> $options
     * @param array<string, mixed> $state Existing provisioner-owned state for idempotent reconciliation.
     * @return array<string, mixed> Provisioner-owned state needed to remove the delegation.
     */
    public function provision(array $zone, array $options = [], array $state = []): array;

    /**
     * @param array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string} $zone
     * @param array<string, mixed> $state
     */
    public function deprovision(array $zone, array $state = []): void;
}
