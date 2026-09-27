<?php

declare(strict_types=1);

namespace Webong\Gateway\Automation;

use InvalidArgumentException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;

final class InMemoryAutomationLanguageRegistry implements AutomationLanguageRegistry
{
    /** @var array<string, array{target: AutomationExecutionTarget, capabilities: array<string, true>}> */
    private array $languages = [];

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
        return isset($this->languages[$language]['capabilities'][$capability->value]);
    }

    public function target(string $language): ?AutomationExecutionTarget
    {
        return $this->languages[$language]['target'] ?? null;
    }

    public function languages(AutomationCapability $capability): array
    {
        return array_keys(array_filter(
            $this->languages,
            static fn (array $language): bool => isset($language['capabilities'][$capability->value]),
        ));
    }
}
