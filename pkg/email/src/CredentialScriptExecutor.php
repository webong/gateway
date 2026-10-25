<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocols\Delivery;

interface CredentialScriptExecutor
{
    /** @param array<string, string> $event */
    public function execute(string $source, array $event): Delivery;
}
