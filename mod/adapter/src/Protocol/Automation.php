<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;

/** Source selected by Laravel for Go or an installed planner extension. */
final readonly class Automation
{
    public function __construct(
        public string $language,
        public string $source,
        public ?string $executor = null,
    ) {
        if (preg_match('/^[a-z][a-z0-9_-]*$/', $language) !== 1) {
            throw new InvalidArgumentException("Invalid gateway automation language [{$language}].");
        }

        if (trim($source) === '') {
            throw new InvalidArgumentException('Gateway automation source is required.');
        }

        if ($executor !== null && ! in_array($executor, ['direct', 'goroutine', 'ergo'], true)) {
            throw new InvalidArgumentException("Invalid gateway automation executor [{$executor}].");
        }
    }

    /** @return array{language: string, source: string, executor?: string} */
    public function toArray(): array
    {
        return array_filter([
            'language' => $this->language,
            'source' => $this->source,
            'executor' => $this->executor,
        ], static fn (mixed $value): bool => $value !== null);
    }
}
