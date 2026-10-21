<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocol\GatewayEvent;

final readonly class ConnectedSMTPAuthenticator implements EmailSubmissionAuthenticator
{
    public function __construct(private MailboxConnectionStore $connections, private ManagedSendingStore $managed) {}

    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant
    {
        if ($event->protocol->value !== 'smtp') { return null; }
        $username = $event->attributes['username'] ?? '';
        $password = $event->attributes['password'] ?? '';
        $sender = $this->managed->authenticate($username, $password);
        if ($sender !== null) {
            if (($sender->document['mode'] ?? null) === 'shared') {
                return ($event->attributes['mechanism'] ?? '') === 'HTTP-Basic'
                    ? new EmailSubmissionGrant($sender->owner.':'.$sender->id, [$this->managed->sharedFromAddress()],
                        sharedRouteId: $sender->id) : null;
            }
            return new EmailSubmissionGrant($sender->owner.':'.$sender->id, [$sender->key]);
        }
        $record = $this->connections->authenticate($username, $password, 'smtp');

        return $record === null ? null : new EmailSubmissionGrant(
            $record->account_id.':'.$record->id, [$record->address], connectionId: $record->id);
    }
}
