<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use Webong\Proxy\Contracts\EndpointProvider;
use Webong\Proxy\Endpoint;

/** @internal */
final readonly class ResolvedRegistryEndpoint
{
    public function __construct(
        public Endpoint $endpoint,
        public EndpointProvider $provider,
    ) {
    }
}
