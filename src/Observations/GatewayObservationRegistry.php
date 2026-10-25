<?php

declare(strict_types=1);

namespace Webong\Gateway\Observations;

use Closure;
use InvalidArgumentException;
use RuntimeException;
use Throwable;
use Webong\Gateway\Protocols\GatewayObservation;

final class GatewayObservationRegistry
{
    /** @var array<string, list<Closure(GatewayObservation): void>> */
    private array $recorders = [];

    /** @param Closure(GatewayObservation): void $recorder */
    public function register(string $type, Closure $recorder): void
    {
        if (preg_match('/^[a-z][a-z0-9.-]{0,63}$/D', $type) !== 1) {
            throw new InvalidArgumentException('Invalid Gateway observation type.');
        }
        $this->recorders[$type][] = $recorder;
    }

    public function record(GatewayObservation $observation): void
    {
        $recorders = $this->recorders[$observation->type] ?? [];
        if ($recorders === []) {
            throw new InvalidArgumentException("Gateway observation type [{$observation->type}] is not installed.");
        }
        $failure = null;
        foreach ($recorders as $recorder) {
            try {
                $recorder($observation);
            } catch (Throwable $exception) {
                $failure ??= $exception;
            }
        }
        if ($failure !== null) {
            throw new RuntimeException('One or more Gateway observation recorders failed.', previous: $failure);
        }
    }
}
