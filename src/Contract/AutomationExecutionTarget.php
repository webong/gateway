<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

enum AutomationExecutionTarget: string
{
    case PLANNER = 'planner';
    case SPINNER = 'spinner';
}
