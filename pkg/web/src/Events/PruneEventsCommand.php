<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use Illuminate\Console\Command;

final class PruneEventsCommand extends Command
{
    protected $signature = 'gateway:prune-events';

    protected $description = 'Delete endpoint events older than the configured retention window';

    public function handle(EventRetention $retention): int
    {
        $deleted = $retention->prune();
        $this->components->info("Deleted {$deleted} expired Gateway events.");

        return self::SUCCESS;
    }
}
