<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Contracts\SubscriptionTargetExtension;
use Webong\Gateway\Protocols\SubscriptionAutomationPlan;
use Webong\Gateway\ResolvedSubscription;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\Endpoint;

/** Owns HTTP target input, named network profiles, and the public subscription representation. */
final readonly class HttpSubscriptionTargetExtension implements SubscriptionTargetExtension
{
    public const KIND = 'http';

    public function __construct(private EndpointHttpTransportResolver $transportResolver) {}

    public function kind(): string { return self::KIND; }
    public function creationRules(): array { return ['url' => ['required', 'url:http,https', 'max:2048'], 'profile' => ['sometimes', 'string', 'min:1', 'max:64']]; }
    public function updateRules(): array { return ['url' => ['sometimes', 'url:http,https', 'max:2048'], 'profile' => ['sometimes', 'nullable', 'string', 'min:1', 'max:64']]; }
    public function authorizeCreate(Request $request): ?JsonResponse { return null; }

    public function createTarget(array $input, string $endpointKey, string $subscriberId): string
    {
        $url = $input['url'] ?? null;
        if (! is_string($url)) { throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.'); }
        self::assertUrl($url);
        return $url;
    }

    public function createTargetOptions(array $input): array
    {
        $profile = $input['profile'] ?? null;
        if ($profile === null) { return []; }
        if (! is_string($profile) || trim($profile) === '' || strlen($profile) > 64) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
        }
        return ['profile' => $profile];
    }

    public function assertDefinition(SubscriptionDefinition $definition): void
    {
        if ($definition->targetKind !== self::KIND) { throw new InvalidArgumentException('Invalid HTTP subscription target kind.'); }
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
            $this->transportResolver->resolve($endpoint, $targetOptions['profile']);
        }
    }

    public function matchesTarget(string $target): bool
    {
        $parts = parse_url($target);
        return is_array($parts) && in_array($parts['scheme'] ?? null, ['http', 'https'], true)
            && is_string($parts['host'] ?? null) && $parts['host'] !== '';
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
            throw new InvalidArgumentException('The HTTP subscription contains unsupported changes.');
        }
        if (isset($changes['type']) && ! in_array($changes['type'], ['relay', 'reply'], true)) {
            throw new InvalidArgumentException('Unsupported subscription type.');
        }
        if (isset($changes['url'])) {
            if (! is_string($changes['url'])) { throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.'); }
            self::assertUrl($changes['url']);
        }
        if (array_key_exists('profile', $changes) && $changes['profile'] !== null
            && (! is_string($changes['profile']) || trim($changes['profile']) === '' || strlen($changes['profile']) > 64)) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
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
            $this->transportResolver->resolve($resolved->endpoint, $changes['profile']);
            return ['profile' => $changes['profile']];
        }

        $options = $resolved->destination->metadata[SubscriptionDefinition::TARGET_OPTIONS_METADATA_KEY] ?? [];
        if (! is_array($options)) { throw new InvalidArgumentException('Persisted Web target options are malformed.'); }
        $legacyProfile = $resolved->destination->metadata['_gateway_profile'] ?? null;
        return ! array_key_exists('profile', $options) && is_string($legacyProfile)
            ? ['profile' => $legacyProfile]
            : [];
    }

    public function validateMetadata(array $metadata): void
    {
        foreach (array_keys($metadata) as $key) {
            if (! is_string($key) || str_starts_with($key, '_')
                || in_array($key, ['subscriber_id', 'delivery_mode', 'target_kind', 'profile'], true)) {
                throw new InvalidArgumentException('Subscription metadata contains a reserved key.');
            }
        }
    }

    public function canReuseReply(SubscriptionDefinition $definition, DestinationRecord $existing): bool { return true; }
    public function allowsAccountCredentials(string $operation, string $type): bool { return $type === 'relay' && in_array($operation, ['create', 'manage', 'match'], true); }
    public function supportsProtocol(string $protocol): bool { return in_array($protocol, ['http', 'websocket', 'webstream'], true); }
    public function plan(DestinationRecord $destination, string $endpointKey, string $protocol): ?SubscriptionAutomationPlan { return null; }
    public function remove(ResolvedSubscription $resolved): void {}

    public static function assertUrl(string $url): void
    {
        $parsed = parse_url($url);
        if (! is_array($parsed) || ! in_array($parsed['scheme'] ?? null, ['http', 'https'], true) || ($parsed['host'] ?? '') === '') {
            throw new InvalidArgumentException('Subscription URL must be an HTTP(S) URL.');
        }
    }

    /** @param array<string, mixed> $options */
    private function assertOptions(array $options): void
    {
        if (array_diff(array_keys($options), ['profile']) !== []) { throw new InvalidArgumentException('Invalid Web target options.'); }
        if (isset($options['profile']) && (! is_string($options['profile']) || trim($options['profile']) === '' || strlen($options['profile']) > 64)) {
            throw new InvalidArgumentException('Subscription profile must be a non-empty string of at most 64 characters.');
        }
    }

    /** @return array<string, mixed> */
    private function options(DestinationRecord $destination): array
    {
        $options = $destination->metadata[SubscriptionDefinition::TARGET_OPTIONS_METADATA_KEY] ?? null;
        if ($options === null && is_string($destination->metadata['_gateway_profile'] ?? null)) {
            $options = ['profile' => $destination->metadata['_gateway_profile']];
        }
        $options ??= [];
        if (! is_array($options)) { throw new InvalidArgumentException('Persisted Web target options are malformed.'); }
        $this->assertOptions($options);
        return $options;
    }
}
