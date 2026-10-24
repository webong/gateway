<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use InvalidArgumentException;

/** Planner-owned lifecycle policy for one dynamic companion process. */
final readonly class CompanionExecutionPolicy
{
    public const string ISOLATED = 'isolated';

    public const string WARMED = 'warmed';

    public function __construct(
        public string $mode = self::ISOLATED,
        public int $maxInvocations = 100,
        public int $maxAgeSeconds = 300,
        public int $idleTimeoutSeconds = 30,
        public int $generation = 0,
    ) {
        if (! in_array($mode, [self::ISOLATED, self::WARMED], true)
            || $maxInvocations < 1 || $maxInvocations > 10000
            || $maxAgeSeconds < 1 || $maxAgeSeconds > 3600
            || $idleTimeoutSeconds < 1 || $idleTimeoutSeconds > 300
            || $idleTimeoutSeconds > $maxAgeSeconds
            || $generation < 0) {
            throw new InvalidArgumentException('Invalid Gateway companion execution policy.');
        }
    }

    /** @param array<string, mixed>|null $document */
    public static function fromDocument(?array $document, int $generation = 0): self
    {
        if ($document === null) {
            return new self(generation: $generation);
        }
        if (array_diff(array_keys($document), [
            'mode', 'max_invocations', 'max_age_seconds', 'idle_timeout_seconds',
        ]) !== []) {
            throw new InvalidArgumentException('Invalid Gateway companion execution policy.');
        }
        $mode = $document['mode'] ?? self::ISOLATED;
        $maxInvocations = $document['max_invocations'] ?? 100;
        $maxAgeSeconds = $document['max_age_seconds'] ?? 300;
        $idleTimeoutSeconds = $document['idle_timeout_seconds'] ?? 30;
        if (! is_string($mode) || ! is_int($maxInvocations) || ! is_int($maxAgeSeconds)
            || ! is_int($idleTimeoutSeconds)) {
            throw new InvalidArgumentException('Invalid Gateway companion execution policy.');
        }

        return new self(
            mode: $mode,
            maxInvocations: $maxInvocations,
            maxAgeSeconds: $maxAgeSeconds,
            idleTimeoutSeconds: $idleTimeoutSeconds,
            generation: $generation,
        );
    }

    /** @return array{mode: string, max_invocations?: int, max_age_seconds?: int, idle_timeout_seconds?: int} */
    public function toArray(): array
    {
        if ($this->mode === self::ISOLATED) {
            return ['mode' => self::ISOLATED];
        }

        return [
            'mode' => self::WARMED,
            'max_invocations' => $this->maxInvocations,
            'max_age_seconds' => $this->maxAgeSeconds,
            'idle_timeout_seconds' => $this->idleTimeoutSeconds,
        ];
    }

    public function key(): string
    {
        return implode(':', [
            $this->mode,
            $this->maxInvocations,
            $this->maxAgeSeconds,
            $this->idleTimeoutSeconds,
            $this->generation,
        ]);
    }
}
