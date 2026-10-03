<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocol\GatewayEvent;

/** Verify POP3 or IMAP credentials and select one deployment-registered upstream. */
interface MailboxAuthenticator
{
    public function authorize(GatewayEvent $event): ?MailboxAccessGrant;
}
