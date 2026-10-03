<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

enum GatewayAction: string
{
    case ACCEPT = 'accept';
    case REJECT = 'reject';
    case RESPOND = 'respond';
    case DELIVER = 'deliver';
    case SUBMIT = 'submit';
    case QUARANTINE = 'quarantine';
    case CLOSE = 'close';
    case STREAM = 'stream';
}
