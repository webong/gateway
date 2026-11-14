<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Closure;
use Throwable;

/** At-least-once delivery. Publishers/consumers deduplicate on the persistent event ID. */
final readonly class QueueOutboxDispatcher
{
    /** @param Closure(string, string, array<string, mixed>): void $publish */
    public function __construct(private QueueScheduler $scheduler, private Closure $publish)
    {
    }

    public function dispatch(int $limit = 100, int $leaseSeconds = 30, int $retrySeconds = 5): int
    {
        // Reserve one at a time so a slow publisher does not consume the leases of a whole batch.
        $delivered = 0;
        for ($i = 0; $i < $limit; ++$i) {
            $events = $this->scheduler->reserveEvents(1, $leaseSeconds);
            if ($events === []) {
                break;
            }
            $event = $events[0];
            try {
                ($this->publish)($event['id'], $event['type'], $event['payload']);
            } catch (Throwable $e) {
                $this->scheduler->retryEvent($event['id'], $event['token'], $retrySeconds);
                throw $e;
            }
            $this->scheduler->acknowledgeEvent($event['id'], $event['token']);
            ++$delivered;
        }
        return $delivered;
    }
}
