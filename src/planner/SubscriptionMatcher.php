<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\MatchRules;
use Webong\Gateway\Protocol\SubscriptionState;

final readonly class SubscriptionMatcher
{
    public function __construct(
        private RequestMatcher $requestMatcher,
    ) {
    }

    /** @param array<string, mixed> $metadata */
    public function matches(IngressRequest $request, array $metadata): bool
    {
        if (SubscriptionState::status($metadata) !== SubscriptionState::ACTIVE) {
            return false;
        }

        $stored = $metadata[MatchRules::METADATA_KEY] ?? null;

        // Destinations created before match rules existed retain their original
        // broadcast behavior.
        if ($stored === null) {
            return true;
        }

        if (! is_array($stored)) {
            return false;
        }

        try {
            $rules = MatchRules::fromArray($stored);
        } catch (InvalidArgumentException) {
            // Persisted rules are validated on registration. If storage is
            // corrupted or manually changed, fail closed for this subscriber.
            return false;
        }

        return $this->requestMatcher->matches($request, $rules);
    }
}
