<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** Deployment-owned lookup of an exact recipient to a trusted adapter. */
interface EmailProviderRouteResolver
{
    public function resolve(string $recipient): ?EmailProviderRoute;
}
