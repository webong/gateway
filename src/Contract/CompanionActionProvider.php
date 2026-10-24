<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

/** One discovered implementation candidate for a companion action. */
interface CompanionActionProvider
{
    public function extension(): string;

    public function mode(): string;

    /** Stable within one process; used to make repeated discovery idempotent. */
    public function providerId(): string;

    public function capability(): CompanionActionCapability;

    public function invoke(CompanionActionInvocation $invocation): CompanionActionResult;
}
