<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Throwable;
use Webong\Gateway\Protocol\GatewayEvent;

final readonly class ConnectedIMAPAuthenticator implements MailboxAuthenticator
{
    public function __construct(private MailboxConnectionStore $connections,
        private EmailUpstreamCatalog $upstreams) {}

    public function authorize(GatewayEvent $event): ?MailboxAccessGrant
    {
        if ($event->protocol->value !== 'imap') { return null; }
        $record = $this->connections->authenticate($event->attributes['username'] ?? '', $event->attributes['password'] ?? '', 'imap');
        if ($record === null) { return null; }
        try { $resolved = $this->connections->resolve($record->id, 'imap'); }
        catch (Throwable) { return null; }
        $upstream = $resolved === null ? null : $this->upstreams->active('imap', $resolved['upstream_id']);
        if ($resolved === null || $upstream === null) { return null; }

        return new MailboxAccessGrant($record->account_id.':'.$record->id,
            $resolved['upstream_id'], $resolved['username'], $resolved['secret'],
            $resolved['mechanism'] === 'XOAUTH2' ? 'XOAUTH2' : 'LOGIN',
            $upstream['address'], $upstream['server_name'], $upstream['network_scope'] ?? 'public');
    }
}
