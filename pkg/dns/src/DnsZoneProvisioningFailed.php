<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use RuntimeException;
use stdClass;

final class DnsZoneProvisioningFailed extends RuntimeException
{
    public function __construct(public readonly stdClass $zone, string $message)
    {
        parent::__construct($message);
    }
}
