<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\WebProxy\Contracts\EndpointProvider;
use Webong\WebProxy\Endpoint;

/** @internal */
final readonly class ResolvedRegistryEndpoint
{
    public function __construct(
        public Endpoint $endpoint,
        public EndpointProvider $provider,
    ) {
    }
}
