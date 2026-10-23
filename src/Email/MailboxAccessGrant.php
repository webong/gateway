<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;

/** A verified mailbox login mapped to a Planner-managed, TLS-only upstream. */
final readonly class MailboxAccessGrant
{
    public function __construct(
        public string $identity,
        public string $upstreamId,
        public string $upstreamUsername,
        public string $upstreamPassword,
        public string $upstreamAuthMechanism = 'LOGIN',
        public string $upstreamAddress = '',
        public string $upstreamServerName = '',
        public string $upstreamNetworkScope = 'public',
    ) {
        if ($identity === '' || strlen($identity) > 255 || trim($identity) !== $identity
            || preg_match('/[\x00-\x1F\x7F]/', $identity) === 1) {
            throw new InvalidArgumentException('Mailbox identity must be a stable non-empty identifier.');
        }
        if (preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $upstreamId) !== 1) {
            throw new InvalidArgumentException('Mailbox upstream ID is invalid.');
        }
        foreach ([$upstreamUsername, $upstreamPassword] as $index => $credential) {
            $limit = $index === 1 && $upstreamAuthMechanism === 'XOAUTH2' ? 4096 : 248;
            if ($credential === '' || strlen($credential) > $limit || preg_match('/[^\x20-\x7E]/', $credential) === 1) {
                throw new InvalidArgumentException('Mailbox upstream credentials are invalid.');
            }
        }
        if (str_contains($upstreamUsername, ' ')) {
            throw new InvalidArgumentException('Mailbox upstream username cannot contain spaces.');
        }
        if (! in_array($upstreamAuthMechanism, ['LOGIN', 'XOAUTH2'], true)) {
            throw new InvalidArgumentException('Mailbox upstream authentication mechanism is invalid.');
        }
        if (($upstreamAddress === '') !== ($upstreamServerName === '')) {
            throw new InvalidArgumentException('Mailbox upstream endpoint is incomplete.');
        }
        if (! in_array($upstreamNetworkScope, ['public', 'private'], true)) {
            throw new InvalidArgumentException('Mailbox upstream network scope is invalid.');
        }
    }
}
