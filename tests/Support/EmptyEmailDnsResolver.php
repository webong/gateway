<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Support;

use Webong\Gateway\Email\MxResolver;
use Webong\Gateway\Email\TxtResolver;

/** Prevent feature tests from consulting the host's DNS configuration. */
final class EmptyEmailDnsResolver implements MxResolver, TxtResolver
{
    public function records(string $name): array
    {
        return [];
    }
}
