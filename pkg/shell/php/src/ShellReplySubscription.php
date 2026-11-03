<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\SubscriptionDefinition;

final class ShellReplySubscription
{
    /** @param array<string, string|list<string>> $rules @param array<string, mixed> $metadata */
    public static function make(
        string $subscriberId,
        string $subscriptionId,
        string $webhookGroup,
        string $routingScope,
        string $routingKey,
        string $hook,
        array $rules = [],
        array $metadata = [],
        ?string $channel = null,
    ): SubscriptionDefinition {
        return new SubscriptionDefinition(
            subscriberId: $subscriberId,
            subscriptionId: $subscriptionId,
            webhookGroup: $webhookGroup,
            routingScope: $routingScope,
            routingKey: $routingKey,
            match: MatchRules::make($rules),
            metadata: $metadata,
            targetOptions: [],
            channel: $channel,
            type: 'reply',
            targetKind: ShellLanguage::ID,
            target: ShellSubscriptionTarget::encode($hook),
        );
    }
}
