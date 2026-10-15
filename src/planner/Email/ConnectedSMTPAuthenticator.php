<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocol\GatewayEvent;

final readonly class ConnectedSMTPAuthenticator implements EmailSubmissionAuthenticator
{
    public function __construct(private MailboxConnectionStore $connections) {}

    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        if ($event->protocol->value !== 'smtp') { return null; }
        $record = $this->connections->authenticate($event->attributes['username'] ?? '', $event->attributes['password'] ?? '', 'smtp');

        return $record === null ? null : new EmailSubmissionGrant(
            $record->account_id.':'.$record->id, [$record->address], connectionId: $record->id);
    }
}
