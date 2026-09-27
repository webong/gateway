<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

enum AutomationCapability: string
{
    case ROUTE = 'route';
    case SCHEDULE = 'schedule';
}
