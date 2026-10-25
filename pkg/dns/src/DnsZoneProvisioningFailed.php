<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use RuntimeException;
use Webong\Proxy\Models\WebProxyEndpoint;

final class DnsZoneProvisioningFailed extends RuntimeException
{
    public function __construct(public readonly WebProxyEndpoint $zone, string $message)
    {
        parent::__construct($message);
    }
}
