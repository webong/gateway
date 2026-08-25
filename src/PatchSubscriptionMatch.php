<?php

declare(strict_types=1);

namespace Webong\NetGateway;

use InvalidArgumentException;
use Webong\NetGateway\Protocol\MatchRules;
use Webong\NetGateway\Protocol\MatchRulesPatch;

final readonly class PatchSubscriptionMatch
{
    public function __construct(
        private SubscriptionRegistry $subscriptions,
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
            static function (ResolvedSubscription $resolved) use ($patch): array {
                $stored = $resolved->destination->metadata[MatchRules::METADATA_KEY] ?? null;
                if ($stored === null) {
                    $current = MatchRules::any();
                } elseif (is_array($stored)) {
                    $current = MatchRules::fromArray($stored);
                } else {
                    throw new InvalidArgumentException('The subscription contains malformed persisted match rules.');
                }

                $updated = $patch->apply($current);

                return ['metadata' => [
                    ...$resolved->destination->metadata,
                    MatchRules::METADATA_KEY => $updated->toArray(),
                ]];
            },
            $channel,
        );
    }
}
