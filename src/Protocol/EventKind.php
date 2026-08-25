<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

enum EventKind: string
{
    case REQUEST = 'request';
    case CONNECT = 'connect';
    case MESSAGE = 'message';
    case CLOSE = 'close';
    case TRANSACTION = 'transaction';
}
