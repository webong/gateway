<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;

/** A stable authenticated identity and its exact permitted sender addresses. */
final readonly class EmailSubmissionGrant
{
    /** @param non-empty-list<string> $authorizedSenders */
    public function __construct(
        public string $identity,
        public array $authorizedSenders,
        public ?int $dailyLimit = null,
        public ?int $dailyByteLimit = null,
    ) {
        if ($identity === '' || strlen($identity) > 255 || trim($identity) !== $identity
            || preg_match('/[\x00-\x1F\x7F]/', $identity) === 1) {
            throw new InvalidArgumentException('Email submission identity must be a stable non-empty identifier.');
        }
        if (! array_is_list($authorizedSenders) || $authorizedSenders === [] || count($authorizedSenders) > 100
            || count(array_filter($authorizedSenders, 'is_string')) !== count($authorizedSenders)) {
            throw new InvalidArgumentException('Email submission grant must contain sender addresses.');
        }
        if ($dailyLimit !== null && ($dailyLimit < 1 || $dailyLimit > 1_000_000)) {
            throw new InvalidArgumentException('Email submission daily limit must be between 1 and 1000000.');
        }
        if ($dailyByteLimit !== null && ($dailyByteLimit < 1 || $dailyByteLimit > 1_000_000_000_000)) {
            throw new InvalidArgumentException('Email submission daily byte limit must be between 1 and 1000000000000.');
        }
    }
}
