<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

enum EventKind: string
{
    case REQUEST = 'request';
    case CONNECT = 'connect';
    case MESSAGE = 'message';
    case CLOSE = 'close';
    case TRANSACTION = 'transaction';
    case RECIPIENT = 'recipient';
    case AUTHENTICATE = 'authenticate';
    case QUERY = 'query';
}
