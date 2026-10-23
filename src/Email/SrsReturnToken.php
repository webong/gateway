<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

final readonly class SrsReturnToken
{
    public function __construct(
        public string $address,
        public string $hash,
        public int $expiresAt,
    ) {}
}
