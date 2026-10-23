<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Console\Command;

final class SendEmailDeliveryStatusEventsCommand extends Command
{
    protected $signature = 'gateway:send-email-delivery-status-events {--limit=25 : Maximum receipts and callbacks to process}';

    protected $description = 'Observe outbound Email receipts and send account delivery-status callbacks';

    public function handle(EmailDeliveryStatusDispatcher $dispatcher): int
    {
        $limit = filter_var($this->option('limit'), FILTER_VALIDATE_INT);
        if ($limit === false || $limit < 1 || $limit > 100) {
            $this->components->error('The limit must be between 1 and 100.');
            return self::FAILURE;
        }
        $result = $dispatcher->dispatch($limit);
        $this->components->info("Observed {$result['observed']} receipts; sent {$result['sent']} callbacks; retried {$result['retried']}; discarded {$result['discarded']}.");
        return self::SUCCESS;
    }
}
