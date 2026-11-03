<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations;

final readonly class MigrationResult
{
    /** @param list<int> $applied */
    public function __construct(public array $applied) {}
}
