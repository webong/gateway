<?php

declare(strict_types=1);

namespace Webong\Gateway\Schedules;

use InvalidArgumentException;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\ScriptScheduleRegistrar;
use Webong\Gateway\Protocols\ScheduleCommand;

final readonly class PersistScriptSchedules implements ScriptScheduleRegistrar
{
    public function __construct(
        private ScheduleStore $store,
        private AutomationLanguageRegistry $languages,
    ) {}

    public function register(string $origin, ScheduleCommand ...$schedules): void
    {
        if (trim($origin) === '' || strlen($origin) > 255) {
            throw new InvalidArgumentException('Script schedule origin must be a non-empty string of at most 255 characters.');
        }
        if ($schedules === [] || count($schedules) > 8) {
            throw new InvalidArgumentException('A script must register between 1 and 8 schedules at a time.');
        }
        $keys = [];
        foreach ($schedules as $schedule) {
            if (isset($keys[$schedule->key])) {
                throw new InvalidArgumentException("A script cannot register duplicate schedule key [{$schedule->key}].");
            }
            $keys[$schedule->key] = true;
            if (! $this->languages->supports($schedule->language, AutomationCapability::SCHEDULE)
                || $this->languages->target($schedule->language) !== AutomationExecutionTarget::SPINNER) {
                throw new InvalidArgumentException("Automation language [{$schedule->language}] is not enabled for schedules.");
            }
        }

        $this->store->upsertScriptSchedules($origin, $schedules);
    }
}
