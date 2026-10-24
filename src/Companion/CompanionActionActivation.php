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
        /** @var array{url: string, sha256: string}|null */
        public ?array $artifact,
        public int $generation,
        public bool $configured,
        public CompanionExecutionPolicy $execution = new CompanionExecutionPolicy,
    ) {}
}
