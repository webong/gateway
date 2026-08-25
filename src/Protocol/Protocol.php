<?php

declare(strict_types=1);

namespace Webong\NetGateway\Protocol;

enum Protocol: string
{
    case HTTP = 'http';
    case WEBSOCKET = 'websocket';
    case SMTP = 'smtp';
}
