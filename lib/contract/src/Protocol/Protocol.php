<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

enum Protocol: string
{
    case HTTP = 'http';
    case WEBSOCKET = 'websocket';
    case WEBSTREAM = 'webstream';
    case SMTP = 'smtp';
    case POP3 = 'pop3';
    case IMAP = 'imap';
    case DNS = 'dns';
}
