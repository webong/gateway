<?php

declare(strict_types=1);

namespace Webong\Gateway;

/** Identity attached to an authenticated Planner registry request. */
final readonly class RegistryPrincipal
{
    private function __construct(public ?string $accountId)
    {
    }

    public static function operator(): self
    {
        return new self(null);
    }

    public static function account(string $accountId): self
    {
        return new self($accountId);
    }

    public function isOperator(): bool
    {
        return $this->accountId === null;
    }
}
