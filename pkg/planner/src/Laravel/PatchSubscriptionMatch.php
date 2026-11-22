<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel;

use InvalidArgumentException;
use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\Protocols\MatchRulesPatch;

final readonly class PatchSubscriptionMatch
{
    public function __construct(
        private SubscriptionRegistry $subscriptions,
        private RequestMatcher $conditions,
    ) {
    }

    public function handle(
        string $endpointKey,
        string $destinationId,
        MatchRulesPatch $patch,
        string $expectedRevision,
        ?string $channel = null,
    ): SubscriptionResource {
        return $this->subscriptions->mutate(
            $endpointKey,
            $destinationId,
            $expectedRevision,
            function (ResolvedSubscription $resolved) use ($patch): array {
                $stored = $resolved->destination->metadata[MatchRules::METADATA_KEY] ?? null;
                if ($stored === null) {
                    $current = MatchRules::any();
                } elseif (is_array($stored)) {
                    $current = MatchRules::fromArray($stored);
                } else {
                    throw new InvalidArgumentException('The subscription contains malformed persisted match rules.');
                }

                $updated = $patch->apply($current);
                $this->conditions->validate($updated);

                return ['metadata' => [
                    ...$resolved->destination->metadata,
                    MatchRules::METADATA_KEY => $updated->toArray(),
                ]];
            },
            $channel,
        );
    }
    public function replace(string $endpointKey, string $destinationId, MatchRules $condition, string $expectedRevision, ?string $channel = null): SubscriptionResource
    {
        $this->conditions->validate($condition);
        return $this->subscriptions->mutate($endpointKey, $destinationId, $expectedRevision,
            static fn (ResolvedSubscription $resolved): array => ['metadata' => [
                ...$resolved->destination->metadata,
                MatchRules::METADATA_KEY => $condition->toArray(),
            ]], $channel);
    }

}
