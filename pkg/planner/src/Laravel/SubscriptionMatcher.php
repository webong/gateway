<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Psr\Log\LoggerInterface;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\Protocols\SubscriptionState;

final readonly class SubscriptionMatcher
{
    public function __construct(
        private RequestMatcher $requestMatcher,
        private ?LoggerInterface $logger = null,
    ) {
    }

    /** @param array<string, mixed> $metadata */
    public function matches(IngressRequest $request, array $metadata): bool
    {
        if (SubscriptionState::status($metadata) !== SubscriptionState::ACTIVE) {
            return false;
        }

        // Only an absent condition is a legacy broadcast subscription.
        if (! array_key_exists(MatchRules::METADATA_KEY, $metadata)) { return true; }
        $stored = $metadata[MatchRules::METADATA_KEY];

        if (! is_array($stored)) {
            $this->logger?->warning('Gateway stored subscription condition is malformed.', ['code' => 'condition_not_object']);
            return false;
        }

        try {
            $rules = MatchRules::fromArray($stored);
        } catch (InvalidArgumentException) {
            $this->logger?->warning('Gateway stored subscription condition is invalid.', ['code' => 'invalid_gateway_condition']);
            // Persisted rules are validated on registration. If storage is
            // corrupted or manually changed, fail closed for this subscriber.
            return false;
        }

        return $this->requestMatcher->matches($request, $rules);
    }
}
