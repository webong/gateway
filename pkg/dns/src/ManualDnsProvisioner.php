<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

final class ManualDnsProvisioner implements DnsProvisioner
{
    public function provision(array $zone, array $options = [], array $state = []): array
    {
        return [];
    }

    public function deprovision(array $zone, array $state = []): void
    {
    }
}
