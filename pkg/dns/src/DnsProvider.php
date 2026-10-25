<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

interface DnsProvider
{
    /**
     * @param array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string} $zone
     * @param array<string, mixed> $options
     * @return array<string, mixed> Provider-owned state needed to remove the delegation.
     */
    public function provision(array $zone, array $options = []): array;

    /**
     * @param array{name: string, nameservers: list<string>, ttl: int, soa_email: ?string} $zone
     * @param array<string, mixed> $state
     */
    public function deprovision(array $zone, array $state = []): void;
}
