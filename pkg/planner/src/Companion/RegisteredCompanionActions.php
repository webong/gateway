<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Contracts\Container\Container;
use InvalidArgumentException;
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final class RegisteredCompanionActions implements CompanionActionRegistry
{
    /** @var array<string, array<string, CompanionActionProvider>> */
    private array $actions = [];

    public function __construct(
        private readonly Container $container,
        private readonly CompanionActionActivationStore $activations,
    ) {}

    public function register(
        string $name,
        string $handler,
        ?string $version = null,
        ?string $digest = null,
        ?string $extension = null,
    ): void {
        if (! is_a($handler, CompanionAction::class, true)) {
            throw new InvalidArgumentException("Gateway companion action handler [{$handler}] must implement CompanionAction.");
        }

        $this->registerProvider(new StaticCompanionActionProvider(
            container: $this->container,
            extensionId: $extension ?? $name,
            handler: $handler,
            providedCapability: new CompanionActionCapability($name, $version, $digest),
        ));
    }

    public function registerProvider(CompanionActionProvider $provider): void
    {
        $capability = $provider->capability();
        $mode = $provider->mode();
        // Reconstructing validates extension and mode even when a third-party
        // provider supplied the candidate object.
        new CompanionActionCapability(
            $capability->name,
            $capability->version,
            $capability->digest,
            $provider->extension(),
            $mode,
        );

        foreach ($this->actions[$capability->name] ?? [] as $candidate) {
            if (! hash_equals($candidate->extension(), $provider->extension())) {
                throw new InvalidArgumentException("Gateway companion action [{$capability->name}] is claimed by multiple extensions.");
            }
        }
        $existing = $this->actions[$capability->name][$mode] ?? null;
        if ($existing !== null) {
            if (hash_equals($existing->providerId(), $provider->providerId())
                && $existing->capability()->toArray() === $capability->toArray()) {
                return;
            }
            throw new InvalidArgumentException("Gateway companion action [{$capability->name}] already has a {$mode} provider.");
        }

        $this->actions[$capability->name][$mode] = $provider;
        ksort($this->actions);
    }

    /** @param iterable<CompanionActionProvider> $providers */
    public function replaceDynamicProviders(iterable $providers): void
    {
        foreach ($this->actions as $name => $candidates) {
            $dynamic = $candidates['dynamic'] ?? null;
            if ($dynamic !== null && method_exists($dynamic, 'close')) {
                $dynamic->close();
            }
            unset($this->actions[$name]['dynamic']);
            if ($this->actions[$name] === []) {
                unset($this->actions[$name]);
            }
        }
        foreach ($providers as $provider) {
            $this->registerProvider($provider);
        }
    }

    public function capabilities(): array
    {
        $capabilities = [];
        foreach (array_keys($this->actions) as $name) {
            try {
                $capabilities[] = $this->resolve($name)['capability'];
            } catch (CompanionActionNotFound|CompanionActionIncompatible) {
                // Capability discovery advertises only enabled, compatible and
                // currently available providers. Status remains visible to CMS.
            }
        }

        return $capabilities;
    }

    public function capability(string $name): ?CompanionActionCapability
    {
        try {
            return $this->resolve($name)['capability'];
        } catch (CompanionActionNotFound|CompanionActionIncompatible) {
            return null;
        }
    }

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $definition = $this->resolve($invocation->action);
        $capability = $definition['capability'];
        if ($invocation->companionVersion !== null && $invocation->companionVersion !== $capability->version) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] version is incompatible.");
        }
        if ($invocation->companionDigest !== null && $invocation->companionDigest !== $capability->digest) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] digest is incompatible.");
        }

        $result = $definition['provider']->invoke($invocation);
        if ($result->executionId !== $invocation->executionId) {
            throw new InvalidArgumentException("Gateway companion action [{$invocation->action}] returned the wrong execution ID.");
        }
        if ($result->status === CompanionActionResult::IN_PROGRESS) {
            throw new InvalidArgumentException("Gateway companion action [{$invocation->action}] cannot return an in-progress result.");
        }

        return $result;
    }

    public function statuses(): array
    {
        $extensions = [];
        foreach ($this->activations->all() as $row) {
            $extensions[$row->key] = true;
        }
        foreach ($this->actions as $providers) {
            foreach ($providers as $provider) {
                $extensions[$provider->extension()] = true;
            }
        }

        $statuses = [];
        foreach (array_keys($extensions) as $extension) {
            $selection = $this->activations->selection($extension);
            $actions = [];
            foreach ($this->actions as $name => $providers) {
                foreach ($providers as $mode => $provider) {
                    if ($provider->extension() === $extension) {
                        $actions[$name][$mode] = $provider->capability()->toArray();
                    }
                }
            }
            ksort($actions);
            $state = ! $selection->enabled ? 'disabled' : 'unavailable';
            if ($selection->enabled && $actions !== []) {
                $state = 'ready';
                foreach ($actions as $providers) {
                    $selected = $providers[$selection->mode] ?? null;
                    if ($selected === null || ! $this->matchesSelection($selected, $selection)) {
                        $state = $selected === null ? 'unavailable' : 'incompatible';
                        break;
                    }
                }
            }
            $statuses[] = [
                'extension' => $extension,
                'enabled' => $selection->enabled,
                'mode' => $selection->mode,
                'version' => $selection->version,
                'digest' => $selection->digest,
                'execution' => $selection->execution->toArray(),
                'generation' => $selection->generation,
                'configured' => $selection->configured,
                'state' => $state,
                'actions' => $actions,
            ];
        }
        usort($statuses, static fn (array $a, array $b): int => $a['extension'] <=> $b['extension']);

        return $statuses;
    }

    /** @return array{provider: CompanionActionProvider, capability: CompanionActionCapability} */
    private function resolve(string $name): array
    {
        $providers = $this->actions[$name] ?? null;
        if ($providers === null || $providers === []) {
            throw new CompanionActionNotFound("Gateway companion action [{$name}] is not installed.");
        }
        $extension = reset($providers)->extension();
        $selection = $this->activations->selection($extension);
        if (! $selection->enabled) {
            throw new CompanionActionNotFound("Gateway companion action [{$name}] is disabled.");
        }
        $provider = $providers[$selection->mode] ?? null;
        if ($provider === null) {
            throw new CompanionActionNotFound("Gateway companion action [{$name}] has no enabled {$selection->mode} provider.");
        }
        $provided = $provider->capability();
        if (! $this->matchesSelection($provided->toArray(), $selection)) {
            throw new CompanionActionIncompatible("Gateway companion action [{$name}] does not match its enabled extension release.");
        }

        return [
            'provider' => $provider,
            'capability' => new CompanionActionCapability(
                name: $provided->name,
                version: $provided->version,
                digest: $provided->digest,
                extension: $extension,
                mode: $selection->mode,
                generation: $selection->generation,
            ),
        ];
    }

    /** @param array<string, mixed> $capability */
    private function matchesSelection(array $capability, CompanionActionActivation $selection): bool
    {
        return ($selection->version === null || $selection->version === ($capability['version'] ?? null))
            && ($selection->digest === null || $selection->digest === ($capability['digest'] ?? null));
    }
}
