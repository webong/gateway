<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocols\Protocol;

/** Adds a network delivery protocol to a native subscription target. */
interface SubscriptionDeliveryTargetExtension
{
    /** The egress protocol used when this target receives a routed event. */
    public function deliveryProtocol(): Protocol;
}
