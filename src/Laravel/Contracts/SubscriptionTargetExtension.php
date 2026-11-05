<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Contracts;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Laravel\ResolvedSubscription;
use Webong\Gateway\Laravel\SubscriptionDefinition;
use Webong\Gateway\Protocols\SubscriptionAutomationPlan;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\Endpoint;

/** Supplies Planner behavior for one destination kind. */
interface SubscriptionTargetExtension
{
    public function kind(): string;

    /** @return array<string, array<mixed>> */
    public function creationRules(): array;

    /** @return array<string, array<mixed>> */
    public function updateRules(): array;

    public function authorizeCreate(Request $request): ?JsonResponse;

    /** @param array<string, mixed> $input */
    public function createTarget(array $input, string $endpointKey, string $subscriberId): string;

    /** @param array<string, mixed> $input @return array<string, mixed> */
    public function createTargetOptions(array $input): array;

    public function assertDefinition(SubscriptionDefinition $definition): void;

    /** @param array<string, mixed> $targetOptions @param array<string, mixed> $metadata */
    public function assertAllowed(string $endpointKey, string $subscriberId, string $target, array $targetOptions, array $metadata, ?Endpoint $endpoint = null): void;

    public function matchesTarget(string $target): bool;

    /** @return array<string, mixed> Extension-owned public target fields. */
    public function resourceFields(string $endpointKey, DestinationRecord $destination): array;

    public function authorizeMutation(Request $request, DestinationRecord $destination): ?JsonResponse;

    /** @param array<string, mixed> $changes */
    public function assertMutationAllowed(ResolvedSubscription $resolved, array $changes): void;

    /** Return the new stored target, or null when unchanged. @param array<string, mixed> $changes */
    public function updateTarget(ResolvedSubscription $resolved, array $changes): ?string;

    /** Return option updates; null values remove options. @param array<string, mixed> $changes @return array<string, mixed> */
    public function updateTargetOptions(ResolvedSubscription $resolved, array $changes): array;

    /** @param array<string, mixed> $metadata */
    public function validateMetadata(array $metadata): void;

    public function canReuseReply(SubscriptionDefinition $definition, DestinationRecord $existing): bool;

    /** Account credentials may manage only capabilities explicitly allowed by the target owner. */
    public function allowsAccountCredentials(string $operation, string $type): bool;

    public function supportsProtocol(string $protocol): bool;

    /** Null means the owning route planner handles this target as a native route destination. */
    public function plan(DestinationRecord $destination, string $endpointKey, string $protocol): ?SubscriptionAutomationPlan;

    public function remove(ResolvedSubscription $resolved): void;
}
