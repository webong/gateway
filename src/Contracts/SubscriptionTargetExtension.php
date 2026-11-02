<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Webong\Gateway\Protocols\SubscriptionAutomationPlan;
use Webong\Gateway\ResolvedSubscription;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Proxy\DestinationRecord;

/**
 * Adds one non-HTTP destination kind to Planner subscriptions.
 *
 * Extensions own their input fields, stored target representation, mutation
 * policy, response fields, and route-plan contribution. Planner owns shared
 * subscription identity, matching, revisions, and persistence.
 */
interface SubscriptionTargetExtension
{
    public function kind(): string;

    /** @return array<string, array<mixed>> Additional creation validation rules keyed by request field. */
    public function creationRules(): array;

    /** @return array<string, array<mixed>> Additional update validation rules keyed by request field. */
    public function updateRules(): array;

    public function authorizeCreate(Request $request): ?JsonResponse;

    /** @param array<string, mixed> $input */
    public function createTarget(array $input, string $endpointKey, string $subscriberId): string;

    public function assertDefinition(SubscriptionDefinition $definition): void;

    /** @param array<string, mixed> $metadata */
    public function assertAllowed(string $endpointKey, string $subscriberId, string $target, array $metadata): void;

    public function matchesTarget(string $target): bool;

    /** @return array<string, mixed> Extension-owned public target fields, excluding target_kind. */
    public function resourceFields(string $endpointKey, DestinationRecord $destination): array;

    public function authorizeMutation(Request $request, DestinationRecord $destination): ?JsonResponse;

    /** @param array<string, mixed> $changes */
    public function assertMutationAllowed(ResolvedSubscription $resolved, array $changes): void;

    /** @param array<string, mixed> $changes Return the new stored target, or null when it is unchanged. */
    public function updateTarget(ResolvedSubscription $resolved, array $changes): ?string;

    /** @param array<string, mixed> $metadata */
    public function validateMetadata(array $metadata): void;

    public function canReuseReply(SubscriptionDefinition $definition, DestinationRecord $existing): bool;

    public function supportsProtocol(string $protocol): bool;

    public function plan(DestinationRecord $destination, string $endpointKey, string $protocol): SubscriptionAutomationPlan;

    public function remove(ResolvedSubscription $resolved): void;
}
