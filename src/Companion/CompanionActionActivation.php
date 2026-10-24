<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

final readonly class CompanionActionActivation
{
    public function __construct(
        public string $extension,
        public bool $enabled,
        public string $mode,
        public ?string $version,
        public ?string $digest,
        public int $generation,
        public bool $configured,
    ) {}
}
