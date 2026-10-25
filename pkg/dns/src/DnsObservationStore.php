<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Carbon\CarbonInterface;
use Illuminate\Contracts\Pagination\CursorPaginator;
use Illuminate\Database\Eloquent\Builder;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;

final class DnsObservationStore
{
    public function events(string $subjectKind, string $subjectId, int $limit, ?string $type, ?string $transport): CursorPaginator
    {
        return $this->query($subjectKind, $subjectId)
            ->when($type !== null && $type !== '', fn (Builder $query) => $query->where('attributes->qtype', strtoupper($type)))
            ->when($transport !== null && $transport !== '', fn (Builder $query) => $query->where('transport', strtolower($transport)))
            ->latest('received_at')
            ->latest('id')
            ->cursorPaginate($limit)
            ->through(DnsObservationResources::event(...));
    }

    /** @return array<string, mixed> */
    public function usage(string $subjectKind, string $subjectId, CarbonInterface $from, CarbonInterface $to): array
    {
        $events = $this->query($subjectKind, $subjectId)
            ->whereBetween('received_at', [$from, $to])
            ->get(['attributes', 'transport', 'error', 'received_at']);

        return [
            'period' => ['from' => $from->toAtomString(), 'to' => $to->toAtomString()],
            'queries' => $events->count(),
            'failures' => $events->whereNotNull('error')->count(),
            'by_type' => $events
                ->groupBy(static fn (GatewayEndpointEvent $event): string => (string) (($event->attributes ?? [])['qtype'] ?? ''))
                ->map(static fn ($events): int => $events->count())->sortKeys(),
            'by_transport' => $events
                ->groupBy(static fn (GatewayEndpointEvent $event): string => (string) ($event->transport ?? ''))
                ->map(static fn ($events): int => $events->count())->sortKeys(),
            'by_response' => $events
                ->groupBy(static fn (GatewayEndpointEvent $event): string => (string) (($event->attributes ?? [])['response_name'] ?? ''))
                ->map(static fn ($events): int => $events->count())->sortKeys(),
            'daily' => $events
                ->groupBy(static fn (GatewayEndpointEvent $event): string => $event->received_at->toDateString())
                ->map(static fn ($events, string $date): array => ['date' => $date, 'queries' => $events->count()])
                ->sortKeys()->values(),
        ];
    }

    /** @return Builder<GatewayEndpointEvent> */
    private function query(string $subjectKind, string $subjectId): Builder
    {
        return GatewayEndpointEvent::query()
            ->where('subject_kind', $subjectKind)
            ->where('subject_id', $subjectId)
            ->where('protocol', 'dns');
    }
}
