<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Illuminate\Http\Client\ConnectionException;
use RuntimeException;
use Webong\Gateway\Deliveries\DeliveryAdminClient;

/** Resolves execution and durable delivery state without exposing callback data. */
final readonly class ScheduleRunOutcome
{
    public function __construct(private DeliveryAdminClient $admin) {}

    /** @return array<string, mixed> */
    public function inspect(object $run): array
    {
        $expected = (int) $run->deliveries_queued;
        $counts = ['queued' => 0, 'deferred' => 0, 'failed' => 0, 'delivered' => 0];
        if (in_array($run->status, ['pending', 'claimed'], true)) {
            return $this->result($run, 'pending', $counts, 0);
        }
        if ($expected === 0) {
            return $this->result($run, 'none', $counts, 0);
        }

        $truncated = false;
        try {
            foreach ($this->admin->nodes() as $node => $_) {
                $response = $this->admin->get('/deliveries', ['event_id' => $run->id, 'limit' => 100], $node);
                $items = $response->json('data');
                if (! $response->ok() || ! is_array($items) || ! array_is_list($items) || count($items) > 100) {
                    throw new RuntimeException('Delivery outcome is unavailable.');
                }
                $truncated = $truncated || count($items) === 100;
                foreach ($items as $item) {
                    if (! is_array($item) || ($item['event_id'] ?? null) !== $run->id
                        || ! is_string($item['id'] ?? null) || preg_match('/\A[0-9a-f]{32}\z/D', $item['id']) !== 1
                        || ! is_int($item['attempts'] ?? null) || ! is_string($item['state'] ?? null)
                        || ! array_key_exists($item['state'], $counts)) {
                        throw new RuntimeException('Delivery outcome is unavailable.');
                    }
                    $counts[$item['state']]++;
                }
            }
        } catch (ConnectionException $error) {
            throw new RuntimeException('Delivery outcome is unavailable.', previous: $error);
        }

        $observed = array_sum($counts);
        $outcome = match (true) {
            $counts['failed'] > 0 => 'failed',
            $truncated || $observed !== $expected => 'unknown',
            $counts['queued'] + $counts['deferred'] > 0 => 'pending',
            default => 'delivered',
        };

        return $this->result($run, $outcome, $counts, $observed);
    }

    /** @param array<string, int> $counts
     *  @return array<string, mixed>
     */
    private function result(object $run, string $outcome, array $counts, int $observed): array
    {
        return [
            'run_id' => $run->id,
            'execution_status' => $run->status,
            'delivery_status' => $outcome,
            'deliveries_queued' => (int) $run->deliveries_queued,
            'deliveries_observed' => $observed,
            'counts' => $counts,
        ];
    }
}
