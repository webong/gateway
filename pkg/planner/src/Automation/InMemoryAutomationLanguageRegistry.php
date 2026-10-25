<?php

declare(strict_types=1);

namespace Webong\Gateway\Automation;

use Closure;
use InvalidArgumentException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;

final class InMemoryAutomationLanguageRegistry implements AutomationLanguageRegistry
{
    /** @var array<string, array{target: AutomationExecutionTarget, capabilities: array<string, true>}> */
    private array $languages = [];

    /** @var array<string, array{target: AutomationExecutionTarget, capabilities: array<string, true>}> */
    private array $dynamic = [];

    private ?string $dynamicSignature = null;

    /** @param null|Closure(): list<array{id: string, target: string, capabilities: list<string>}> $dynamicLanguages */
    public function __construct(private readonly ?Closure $dynamicLanguages = null) {}

    public function register(
        string $language,
        AutomationExecutionTarget $target,
        AutomationCapability ...$capabilities,
    ): void {
        if (preg_match('/^[a-z][a-z0-9_-]*$/', $language) !== 1) {
            throw new InvalidArgumentException("Invalid gateway automation language [{$language}].");
        }

        if ($capabilities === []) {
            throw new InvalidArgumentException("Gateway automation language [{$language}] requires a capability.");
        }

        $registeredTarget = $this->languages[$language]['target'] ?? null;
        if ($registeredTarget !== null && $registeredTarget !== $target) {
            throw new InvalidArgumentException("Gateway automation language [{$language}] cannot use multiple execution targets.");
        }

        $this->languages[$language]['target'] = $target;
        foreach ($capabilities as $capability) {
            $this->languages[$language]['capabilities'][$capability->value] = true;
        }

        ksort($this->languages);
    }

    public function supports(string $language, AutomationCapability $capability): bool
    {
        $this->refreshDynamic();

        return isset($this->definition($language)['capabilities'][$capability->value]);
    }

    public function target(string $language): ?AutomationExecutionTarget
    {
        $this->refreshDynamic();

        return $this->definition($language)['target'] ?? null;
    }

    public function languages(AutomationCapability $capability): array
    {
        $this->refreshDynamic();
        $languages = [...$this->languages, ...$this->dynamic];

        return array_keys(array_filter(
            $languages,
            static fn (array $language): bool => isset($language['capabilities'][$capability->value]),
        ));
    }

    /** @return array{target: AutomationExecutionTarget, capabilities: array<string, true>}|null */
    private function definition(string $language): ?array
    {
        return $this->dynamic[$language] ?? $this->languages[$language] ?? null;
    }

    private function refreshDynamic(): void
    {
        if ($this->dynamicLanguages === null) {
            return;
        }
        try {
            $definitions = ($this->dynamicLanguages)();
        } catch (\Throwable $exception) {
            $this->dynamic = [];
            $this->dynamicSignature = null;
            throw $exception;
        }
        $signature = hash('sha256', json_encode($definitions, JSON_THROW_ON_ERROR));
        if ($signature === $this->dynamicSignature) {
            return;
        }
        $dynamic = [];
        foreach ($definitions as $definition) {
            $language = $definition['id'];
            $target = AutomationExecutionTarget::tryFrom($definition['target']);
            $capabilities = array_map(
                static fn (string $capability): ?AutomationCapability => AutomationCapability::tryFrom($capability),
                $definition['capabilities'],
            );
            if ($target === null || in_array(null, $capabilities, true)) {
                throw new InvalidArgumentException("Invalid dynamic gateway automation language [{$language}].");
            }
            $dynamic[$language] = [
                'target' => $target,
                'capabilities' => array_fill_keys(array_map(
                    static fn (AutomationCapability $capability): string => $capability->value,
                    $capabilities,
                ), true),
            ];
        }
        ksort($dynamic, SORT_STRING);
        $this->dynamic = $dynamic;
        $this->dynamicSignature = $signature;
    }
}
