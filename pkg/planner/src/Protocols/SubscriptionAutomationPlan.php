<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

final readonly class SubscriptionAutomationPlan
{
    /** @param array<string, mixed> $metadata */
    public function __construct(
        public Automation $automation,
        public array $metadata = [],
    ) {
    }
}
