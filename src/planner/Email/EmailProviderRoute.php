<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;

/** A deployment-owned hosted mailbox destination; no network address or secret. */
final readonly class EmailProviderRoute
{
    public function __construct(
        public string $accountId,
        public string $adapter,
        public string $mailboxKey,
    ) {
        if ($accountId === '' || strlen($accountId) > 255 || preg_match('/[\x00-\x1F\x7F]/', $accountId) === 1
            || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $adapter) !== 1 || ! str_contains($adapter, '.')
            || str_starts_with($adapter, 'gateway.') || $adapter === 'mailbox.smtp'
            || $mailboxKey === '' || strlen($mailboxKey) > 320
            || preg_match('/[\x00-\x1F\x7F]/', $mailboxKey) === 1) {
            throw new InvalidArgumentException('Hosted mailbox route is invalid.');
        }
    }
}
