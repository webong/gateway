<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Console\Command;

final class PruneCompanionActionExecutionsCommand extends Command
{
    protected $signature = 'gateway:prune-companion-actions';

    protected $description = 'Delete companion action execution records outside the idempotency window';

    public function handle(CompanionActionExecutionStore $executions): int
    {
        $days = (int) config('gateway.companion_actions.retention_days', 30);
        $deleted = $executions->prune($days);
        $this->components->info("Deleted {$deleted} expired companion action executions.");

        return self::SUCCESS;
    }
}
