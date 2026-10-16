<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Console\Command;
use InvalidArgumentException;

final class PruneEmailSubmissionUsageCommand extends Command
{
    protected $signature = 'gateway:prune-email-submission-usage';

    protected $description = 'Delete old account and sender-domain Email submission counters';

    public function handle(EmailSubmissionQuota $quota, EmailHttpSubmissionStore $submissions,
        EmailRuntimePolicyStore $runtimePolicy): int
    {
        $days = $runtimePolicy->current()['submission_usage_retention_days'];
        if ($days < 1) {
            throw new InvalidArgumentException('Gateway Email submission usage retention must be positive.');
        }
        $deleted = $quota->prune($days);
        $this->components->info("Deleted {$deleted} expired Email submission usage rows.");
        $keys = $submissions->prune();
        $this->components->info("Deleted {$keys} expired HTTP Email submission keys.");

        return self::SUCCESS;
    }
}
