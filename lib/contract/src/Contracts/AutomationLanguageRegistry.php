<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

interface AutomationLanguageRegistry
{
    public function register(
        string $language,
        AutomationExecutionTarget $target,
        AutomationCapability ...$capabilities,
    ): void;

    public function supports(string $language, AutomationCapability $capability): bool;

    public function target(string $language): ?AutomationExecutionTarget;

    /** @return list<string> */
    public function languages(AutomationCapability $capability): array;
}
