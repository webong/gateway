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
        public ?string $extension = null,
        public ?string $mode = null,
        public ?int $generation = null,
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
        if ($extension !== null && (preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $extension) !== 1)) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] extension is invalid.");
        }
        if ($mode !== null && ! in_array($mode, ['static', 'dynamic'], true)) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] mode is invalid.");
        }
        if ($generation !== null && $generation < 0) {
            throw new InvalidArgumentException("Gateway companion action [{$name}] generation is invalid.");
        }
    }

    /** @return array{name: string, version?: string, digest?: string, extension?: string, mode?: string, generation?: int} */
    public function toArray(): array
    {
        return array_filter([
            'name' => $this->name,
            'version' => $this->version,
            'digest' => $this->digest,
            'extension' => $this->extension,
            'mode' => $this->mode,
            'generation' => $this->generation,
        ], static fn (mixed $value): bool => $value !== null);
    }
}
