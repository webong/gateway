<?php

declare(strict_types=1);

namespace Webong\NetGateway\Exceptions;

use RuntimeException;

final class SubscriptionRevisionMismatchException extends RuntimeException
{
    public function __construct(public readonly string $currentRevision)
    {
        parent::__construct('The subscription changed after it was read.');
    }
}
