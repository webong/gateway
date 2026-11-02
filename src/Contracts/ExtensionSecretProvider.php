<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

/** Provides one Extension's private values through Planner's generic secret API. */
interface ExtensionSecretProvider
{
    public function identity(): string;

    /** @return array{reference: string, version: int, value: array<string, mixed>}|null */
    public function resolve(string $reference): ?array;
}
