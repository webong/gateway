<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

final class ManualDnsProvider implements DnsProvider
{
    public function provision(array $zone, array $options = []): array
    {
        return [];
    }

    public function deprovision(array $zone, array $state = []): void
    {
    }
}
