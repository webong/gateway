<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final class CreateTicketCompanionAction implements CompanionAction
{
    public static int $calls = 0;

    public function handle(CompanionActionInvocation $invocation): CompanionActionResult
    {
        self::$calls++;

        return CompanionActionResult::succeeded($invocation->executionId, [
            'ticket_id' => 'ticket-'.self::$calls,
            'subject' => $invocation->input['subject'] ?? null,
        ]);
    }
}
