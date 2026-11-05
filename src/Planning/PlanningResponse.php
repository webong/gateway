<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

final readonly class PlanningResponse
{
    public function __construct(
        public array $payload,
        public int $status = 200,
        public array $headers = [],
    ) {}
}
