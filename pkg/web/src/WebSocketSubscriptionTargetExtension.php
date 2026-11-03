<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Contracts\SubscriptionDeliveryTargetExtension;
use Webong\Gateway\Contracts\SubscriptionTargetExtension;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\SubscriptionAutomationPlan;
use Webong\Gateway\ResolvedSubscription;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\Endpoint;

/** Routes queued HTTP events to an application WebSocket endpoint. */
final readonly class WebSocketSubscriptionTargetExtension implements SubscriptionTargetExtension, SubscriptionDeliveryTargetExtension
{
    public const KIND = 'websocket';

    public function __construct(private EndpointHttpTransportResolver $transportResolver) {}

    public function kind(): string { return self::KIND; }

    public function deliveryProtocol(): Protocol { return Protocol::WEBSOCKET; }

    public function creationRules(): array
    {
        return ['url' => ['required', 'url:ws,wss', 'max:2048'], 'profile' => ['sometimes', 'string', 'min:1', 'max:64']];
    }

    public function updateRules(): array
    {
        return ['url' => ['sometimes', 'url:ws,wss', 'max:2048'], 'profile' => ['sometimes', 'nullable', 'string', 'min:1', 'max:64']];
    }

    public function authorizeCreate(Request $request): ?JsonResponse { return null; }

    public function createTarget(array $input, string $endpointKey, string $subscriberId): string
    {
        $url = $input['url'] ?? null;
        if (! is_string($url)) {
            throw new InvalidArgumentException('WebSocket target URL must use ws:// or wss://.');
        }
        self::assertUrl($url);

        return $url;
    }

    public function createTargetOptions(array $input): array
    {
        $profile = $input['profile'] ?? null;
        if ($profile === null) { return []; }
        self::assertProfile($profile);

        return ['profile' => $profile];
    }

    public function assertDefinition(SubscriptionDefinition $definition): void
    {
        if ($definition->targetKind !== self::KIND || $definition->type !== 'relay') {
            throw new InvalidArgumentException('WebSocket targets support relay subscriptions only.');
        }
        self::assertUrl($definition->target);
        $this->assertOptions($definition->targetOptions);
        $this->validateMetadata($definition->metadata);
    }

    public function assertAllowed(string $endpointKey, string $subscriberId, string $target, array $targetOptions, array $metadata, ?Endpoint $endpoint = null): void
    {
        self::assertUrl($target);
        $this->assertOptions($targetOptions);
        $this->validateMetadata($metadata);
        if (isset($targetOptions['profile']) && $endpoint !== null) {
            $this->assertProfileCompatible($endpoint, $targetOptions['profile']);
        }
    }

    public function matchesTarget(string $target): bool
    {
        $parts = parse_url($target);
        return is_array($parts) && in_array($parts['scheme'] ?? null, ['ws', 'wss'], true)
            && is_string($parts['host'] ?? null) && $parts['host'] !== ''
            && ! isset($parts['user']) && ! isset($parts['fragment']);
    }

    public function resourceFields(string $endpointKey, DestinationRecord $destination): array
    {
        $options = $this->options($destination);
        return ['url' => $destination->target, 'profile' => $options['profile'] ?? null];
    }

    public function authorizeMutation(Request $request, DestinationRecord $destination): ?JsonResponse { return null; }

    public function assertMutationAllowed(ResolvedSubscription $resolved, array $changes): void
    {
        if (array_diff(array_keys($changes), ['url', 'profile', 'type']) !== []) {
            throw new InvalidArgumentException('The WebSocket subscription contains unsupported changes.');
        }
        if (isset($changes['type']) && $changes['type'] !== 'relay') {
            throw new InvalidArgumentException('WebSocket targets support relay subscriptions only.');
        }
        if (isset($changes['url'])) {
            if (! is_string($changes['url'])) {
                throw new InvalidArgumentException('WebSocket target URL must use ws:// or wss://.');
            }
            self::assertUrl($changes['url']);
        }
        if (array_key_exists('profile', $changes) && $changes['profile'] !== null) {
            self::assertProfile($changes['profile']);
        }
    }

    public function updateTarget(ResolvedSubscription $resolved, array $changes): ?string
    {
        return array_key_exists('url', $changes) ? $changes['url'] : null;
    }

    public function updateTargetOptions(ResolvedSubscription $resolved, array $changes): array
    {
        if (array_key_exists('profile', $changes)) {
            if ($changes['profile'] === null) { return ['profile' => null]; }
            self::assertProfile($changes['profile']);
            $this->assertProfileCompatible($resolved->endpoint, $changes['profile']);
            return ['profile' => $changes['profile']];
        }

        return [];
    }

    public function validateMetadata(array $metadata): void
    {
        foreach (array_keys($metadata) as $key) {
            if (! is_string($key) || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode', 'target_kind', 'profile'], true)) {
                throw new InvalidArgumentException('WebSocket subscription metadata contains a reserved key.');
            }
        }
    }

    public function canReuseReply(SubscriptionDefinition $definition, DestinationRecord $existing): bool { return false; }

    public function allowsAccountCredentials(string $operation, string $type): bool
    {
        return $type === 'relay' && in_array($operation, ['create', 'manage', 'match'], true);
    }

    public function supportsProtocol(string $protocol): bool { return $protocol === Protocol::HTTP->value; }

    public function plan(DestinationRecord $destination, string $endpointKey, string $protocol): ?SubscriptionAutomationPlan { return null; }

    public function remove(ResolvedSubscription $resolved): void {}

    public static function assertUrl(string $url): void
    {
        $parsed = parse_url($url);
        if (! is_array($parsed) || ! in_array($parsed['scheme'] ?? null, ['ws', 'wss'], true)
            || ! is_string($parsed['host'] ?? null) || $parsed['host'] === ''
            || isset($parsed['user']) || isset($parsed['fragment'])) {
            throw new InvalidArgumentException('WebSocket target URL must be a ws:// or wss:// URL without user info or fragments.');
        }
    }

    private static function assertProfile(mixed $profile): void
    {
        if (! is_string($profile) || trim($profile) === '' || strlen($profile) > 64) {
            throw new InvalidArgumentException('WebSocket transport profile must be a non-empty string of at most 64 characters.');
        }
    }

    /** @param array<string, mixed> $options */
    private function assertOptions(array $options): void
    {
        if (array_diff(array_keys($options), ['profile']) !== []) {
            throw new InvalidArgumentException('Invalid WebSocket target options.');
        }
        if (isset($options['profile'])) { self::assertProfile($options['profile']); }
    }

    private function assertProfileCompatible(Endpoint $endpoint, string $profile): void
    {
        $transport = $this->transportResolver->resolve($endpoint, $profile);
        if ($transport->httpProxy !== null || $transport->httpsProxy !== null) {
            throw new InvalidArgumentException('WebSocket delivery profiles cannot configure an HTTP proxy because it bypasses destination network checks.');
        }
    }

    /** @return array<string, mixed> */
    private function options(DestinationRecord $destination): array
    {
        $options = $destination->metadata[SubscriptionDefinition::TARGET_OPTIONS_METADATA_KEY] ?? [];
        if (! is_array($options)) { throw new InvalidArgumentException('Persisted WebSocket target options are malformed.'); }
        $this->assertOptions($options);
        return $options;
    }
}
