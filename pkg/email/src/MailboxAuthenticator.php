<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocols\GatewayEvent;

/** Verify POP3 or IMAP credentials and select a Planner-approved upstream. */
interface MailboxAuthenticator
{
    public function authorize(GatewayEvent $event): ?MailboxAccessGrant;
}
