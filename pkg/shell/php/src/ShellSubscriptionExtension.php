<?php

declare(strict_types=1);

namespace Webong\Gateway\Shell;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Contracts\SubscriptionTargetExtension;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\SubscriptionAutomationPlan;
use Webong\Gateway\Protocols\SubscriptionState;
use Webong\Gateway\ResolvedSubscription;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Proxy\DestinationRecord;

final readonly class ShellSubscriptionExtension implements SubscriptionTargetExtension
{
    public function __construct(
        private ShellOperatorAuthenticator $operator,
        private ShellSubscriptionPolicy $policy,
        private ShellSshExecutionStore $sshExecutions,
    ) {
    }

    public function kind(): string
    {
        return ShellLanguage::ID;
    }

    public function creationRules(): array
    {
        return [
            'hook' => ['required', 'string', 'min:1', 'max:1024'],
            'ssh' => ['sometimes', 'array'],
        ];
    }

    public function updateRules(): array
    {
        return ['hook' => ['sometimes', 'string', 'min:1', 'max:1024']];
    }

    public function authorizeCreate(Request $request): ?JsonResponse
    {
        return $this->operator->authorize($request);
    }

    public function createTarget(array $input, string $endpointKey, string $subscriberId): string
    {
        $hook = $input['hook'] ?? null;
        if (! is_string($hook) || ($input['type'] ?? 'relay') !== 'reply') {
            throw new InvalidArgumentException('This subscription target requires a reply hook.');
        }
        ShellSubscriptionTarget::assertHook($hook);

        if (! isset($input['ssh'])) {
            return ShellSubscriptionTarget::encode($hook);
        }

        return ShellSubscriptionTarget::encodeSsh(
            $this->sshExecutions->create($endpointKey, $subscriberId, $hook, $input['ssh'])->id,
            $hook,
        );
    }

    public function assertDefinition(SubscriptionDefinition $definition): void
    {
        if ($definition->type !== 'reply' || $definition->target === null || $definition->profile !== null) {
            throw new InvalidArgumentException('This subscription target supports replies without HTTP profiles.');
        }
        $hook = ShellSubscriptionTarget::hook($definition->target);
        $this->validateMetadata($definition->metadata);
    }

    public function assertAllowed(string $endpointKey, string $subscriberId, string $target, array $metadata): void
    {
        $hook = ShellSubscriptionTarget::hook($target);
        $sshId = ShellSubscriptionTarget::sshId($target);
        $sshIdentity = $sshId === null ? null : $this->sshExecutions->publicIdentity($sshId);
        if ($sshId !== null && $sshIdentity === null) {
            throw new InvalidArgumentException('The SSH execution reference is unavailable.');
        }
        $this->policy->assertAllowed($endpointKey, $subscriberId, $hook, $sshIdentity);
    }

    public function matchesTarget(string $target): bool
    {
        return ShellSubscriptionTarget::isShell($target);
    }

    public function resourceFields(string $endpointKey, DestinationRecord $destination): array
    {
        $sshId = ShellSubscriptionTarget::sshId($destination->target);

        return [
            'url' => null,
            'hook' => ShellSubscriptionTarget::hook($destination->target),
            'ssh' => $sshId === null ? null : $this->sshExecutions->publicIdentity($sshId),
            'profile' => null,
        ];
    }

    public function authorizeMutation(Request $request, DestinationRecord $destination): ?JsonResponse
    {
        return $this->operator->authorize($request);
    }

    public function assertMutationAllowed(ResolvedSubscription $resolved, array $changes): void
    {
        if (array_key_exists('url', $changes) || array_key_exists('profile', $changes)
            || (($changes['type'] ?? null) !== null && $changes['type'] !== 'reply')) {
            throw new InvalidArgumentException('This subscription target accepts only reply-target updates.');
        }
    }

    public function updateTarget(ResolvedSubscription $resolved, array $changes): ?string
    {
        if (! array_key_exists('hook', $changes)) {
            return null;
        }

        $target = $resolved->destination->target;
        if (ShellSubscriptionTarget::sshId($target) !== null) {
            throw new InvalidArgumentException('SSH target identity is immutable; create a new subscription to change its hook.');
        }
        $hook = $changes['hook'];
        if (! is_string($hook)) {
            throw new InvalidArgumentException('The hook is invalid.');
        }
        ShellSubscriptionTarget::assertHook($hook);
        $subscriberId = $resolved->destination->metadata['subscriber_id'] ?? $resolved->destination->owner_id;
        if (! is_string($subscriberId)) {
            throw new InvalidArgumentException('The subscription contains an invalid subscriber identity.');
        }
        $this->policy->assertAllowed($resolved->endpoint->record->endpoint_key, $subscriberId, $hook);

        return ShellSubscriptionTarget::encode($hook);
    }

    public function validateMetadata(array $metadata): void
    {
        foreach (array_keys($metadata) as $key) {
            if (! is_string($key)) {
                throw new InvalidArgumentException('Subscription metadata keys must be strings.');
            }
            ShellSubscriptionTarget::assertMetadataKey($key);
        }
    }

    public function canReuseReply(SubscriptionDefinition $definition, DestinationRecord $existing): bool
    {
        return ShellSubscriptionTarget::sshId($definition->target ?? '') === null
            && ShellSubscriptionTarget::sshId($existing->target) === null;
    }

    public function supportsProtocol(string $protocol): bool
    {
        return $protocol === 'http';
    }

    public function plan(DestinationRecord $destination, string $endpointKey, string $protocol): SubscriptionAutomationPlan
    {
        if (! $this->supportsProtocol($protocol)) {
            throw new InvalidArgumentException('This subscription target does not support this ingress protocol.');
        }
        $hook = ShellSubscriptionTarget::hook($destination->target);
        $subscriberId = $destination->metadata['subscriber_id'] ?? $destination->owner_id;
        if (! is_string($subscriberId)) {
            throw new InvalidArgumentException('The subscription contains an invalid subscriber identity.');
        }
        $sshId = ShellSubscriptionTarget::sshId($destination->target);
        if ($sshId !== null && $this->sshExecutions->findForPlan($sshId, $endpointKey, $subscriberId, $hook) === null) {
            throw new InvalidArgumentException('The SSH execution reference is unavailable.');
        }
        $sshIdentity = $sshId === null ? null : $this->sshExecutions->publicIdentity($sshId);
        $this->policy->assertAllowed($endpointKey, $subscriberId, $hook, $sshIdentity);

        return new SubscriptionAutomationPlan(
            automation: new Automation(language: ShellLanguage::ID, source: $sshId === null ? $hook : 'ssh:'.$sshId),
            metadata: $sshId === null ? [] : ['shell_hook' => $hook],
        );
    }

    public function remove(ResolvedSubscription $resolved): void
    {
        if (($id = ShellSubscriptionTarget::sshId($resolved->destination->target)) !== null) {
            $this->sshExecutions->revoke($id);
        }
    }

    public function rotateCredential(ResolvedSubscription $resolved, string $privateKey): void
    {
        $id = ShellSubscriptionTarget::sshId($resolved->destination->target);
        if ($id === null || SubscriptionState::status($resolved->destination->metadata) === SubscriptionState::REMOVED) {
            throw new InvalidArgumentException('The subscription has no active SSH credential.');
        }
        $this->sshExecutions->rotate($id, $privateKey);
    }

}
