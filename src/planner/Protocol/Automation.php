<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

/** Source selected by Laravel and executed exclusively by the Go router. */
final readonly class Automation
{
    public const WEBHOOKSCRIPT = 'webhookscript';
    public const LUA = 'lua';
    public const JAVASCRIPT = 'javascript';

    public function __construct(
        public string $language,
        public string $source,
    ) {
        if (! in_array($language, [self::WEBHOOKSCRIPT, self::LUA, self::JAVASCRIPT], true)) {
            throw new InvalidArgumentException("Unsupported gateway automation language [{$language}].");
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
