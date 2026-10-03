<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionGrant;
use Webong\Gateway\Protocol\GatewayEvent;

/** Fixed credentials for the isolated container integration fixture only. */
final class EmailHttpSmokeAuthenticator implements EmailSubmissionAuthenticator
{
    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        if (($event->attributes['password'] ?? null) !== 'smoke-secret') {
            return null;
        }

        return match ($event->attributes['username'] ?? null) {
            'smoke-sender' => new EmailSubmissionGrant('container-smoke-sender', ['sender@example.test'], dailyLimit: 1),
            'smoke-second' => new EmailSubmissionGrant('container-smoke-second', ['second@example.test'], dailyLimit: 1),
            'smoke-notice' => new EmailSubmissionGrant('container-smoke-notice', ['notice@other.test'], dailyLimit: 1),
            default => null,
        };
    }
}
