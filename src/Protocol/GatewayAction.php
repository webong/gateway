<?php

declare(strict_types=1);

namespace Webong\NetGateway\Protocol;

enum GatewayAction: string
{
    case ACCEPT = 'accept';
    case REJECT = 'reject';
    case RESPOND = 'respond';
    case DELIVER = 'deliver';
    case CLOSE = 'close';
}
