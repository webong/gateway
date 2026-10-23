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
    ) {
        if (preg_match('/^[a-z][a-z0-9_-]*$/', $language) !== 1) {
            throw new InvalidArgumentException("Invalid gateway automation language [{$language}].");
        }

        if (trim($source) === '') {
            throw new InvalidArgumentException('Gateway automation source is required.');
        }
    }

    /** @return array{language: string, source: string} */
    public function toArray(): array
    {
        return ['language' => $this->language, 'source' => $this->source];
    }
}
