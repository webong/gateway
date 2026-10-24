<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;

final readonly class CompanionActionCapability
{
    public function __construct(
        public string $name,
        public ?string $version = null,
        public ?string $digest = null,
    ) {
        if (preg_match('/\A[a-z0-9]+(?:[._-][a-z0-9]+)*\z/D', $name) !== 1 || strlen($name) > 160) {
            throw new InvalidArgumentException("Invalid Gateway companion action [{$name}].");
        }
        if (($version === null || trim($version) === '') && ($digest === null || trim($digest) === '')) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] requires a version or digest.");
        }
        if ($version !== null && strlen($version) > 64) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] version is too long.");
        }
        if ($digest !== null && preg_match('/\A[a-f0-9]{64}\z/D', $digest) !== 1) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] digest must be a lowercase SHA-256 digest.");
        }
    }

    /** @return array{name: string, version?: string, digest?: string} */
    public function toArray(): array
    {
        return array_filter([
            'name' => $this->name,
            'version' => $this->version,
            'digest' => $this->digest,
        ], static fn (mixed $value): bool => $value !== null);
    }
}
