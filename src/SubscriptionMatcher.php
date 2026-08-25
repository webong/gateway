<?php

declare(strict_types=1);

namespace Webong\WebRelay;

use InvalidArgumentException;
use Webong\WebRelay\Protocol\IngressRequest;
use Webong\WebRelay\Protocol\MatchRules;

final readonly class SubscriptionMatcher
{
    public function __construct(
        private RequestMatcher $requestMatcher,
    ) {
    }

    /** @param array<string, mixed> $metadata */
    public function matches(IngressRequest $request, array $metadata): bool
    {
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
