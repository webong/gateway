<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use Illuminate\Console\Command;

final class SendScheduleNotificationsCommand extends Command
{
    protected $signature = 'gateway:send-schedule-notifications {--limit=25 : Maximum due runs to inspect}';

    protected $description = 'Check completed schedule runs and notify operators of failures';

    public function handle(ScheduleNotificationDispatcher $dispatcher): int
    {
        $limit = filter_var($this->option('limit'), FILTER_VALIDATE_INT);
        if ($limit === false || $limit < 1 || $limit > 100) {
            $this->components->error('The limit must be between 1 and 100.');

            return self::FAILURE;
        }
        $result = $dispatcher->dispatch($limit);
        $this->components->info("Checked {$result['checked']} runs; sent {$result['sent']} alerts; deferred {$result['deferred']}; resolved {$result['resolved']}.");

        return self::SUCCESS;
    }
}
