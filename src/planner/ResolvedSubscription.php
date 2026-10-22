<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Proxy\Contracts\EndpointProvider;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\Endpoint;

/** @internal */
final readonly class ResolvedSubscription
{
    public function __construct(
        public Endpoint $endpoint,
        public EndpointProvider $provider,
        public DestinationRecord $destination,
    ) {
    }
}
