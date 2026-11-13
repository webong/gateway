<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use InvalidArgumentException;
use Webong\Gateway\Laravel\HttpSubscriptionTargetExtension;
use Webong\Gateway\Laravel\SubscriptionDefinition;
use Webong\Gateway\Protocols\MatchRules;

/** Web DSL convenience that compiles HTTP subscriptions into Planner's generic target contract. */
final class WebSubscriptionDefinition
{
    /** @param array<string, string|list<string>> $rules @param array<string, mixed> $metadata */
    public static function matching(
        string $subscriberId, string $subscriptionId, string $webhookGroup, string $routingScope,
        string $routingKey, string $url, array $rules, array $metadata = [], ?string $channel = null,
        string $type = 'relay', ?string $profile = null,
    ): SubscriptionDefinition {
        HttpSubscriptionTargetExtension::assertUrl($url);
        if ($profile !== null && (trim($profile) === '' || strlen($profile) > 64)) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
        }

        return new SubscriptionDefinition(
            subscriberId: $subscriberId, subscriptionId: $subscriptionId, group: $webhookGroup,
            routingScope: $routingScope, routingKey: $routingKey, targetKind: HttpSubscriptionTargetExtension::KIND,
            target: $url, match: MatchRules::make($rules), metadata: $metadata,
            targetOptions: $profile === null ? [] : ['profile' => $profile], channel: $channel, type: $type,
        );
    }
}
