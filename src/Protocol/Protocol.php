<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

enum Protocol: string
{
    case HTTP = 'http';
    case WEBSOCKET = 'websocket';
    case SMTP = 'smtp';
}
