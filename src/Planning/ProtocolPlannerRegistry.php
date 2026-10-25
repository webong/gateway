<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use Closure;
use LogicException;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocols\Protocol;

/** Selects the Planner Extension that owns policy for a protocol. */
final class ProtocolPlannerRegistry
{
    /** @var array<string, Closure(): ProtocolPlanner> */
    private array $factories = [];

    /** @param Closure(): ProtocolPlanner $factory */
    public function register(Protocol $protocol, Closure $factory): void
    {
        if (isset($this->factories[$protocol->value])) {
            throw new LogicException("A Planner module already owns protocol [{$protocol->value}].");
        }

        $this->factories[$protocol->value] = $factory;
    }

    public function resolve(Protocol $protocol): ?ProtocolPlanner
    {
        return isset($this->factories[$protocol->value])
            ? ($this->factories[$protocol->value])()
            : null;
    }
}
