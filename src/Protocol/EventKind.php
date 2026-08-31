<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

enum EventKind: string
{
    case REQUEST = 'request';
    case CONNECT = 'connect';
    case MESSAGE = 'message';
    case CLOSE = 'close';
    case TRANSACTION = 'transaction';
    case AUTHENTICATE = 'authenticate';
    case QUERY = 'query';
}
