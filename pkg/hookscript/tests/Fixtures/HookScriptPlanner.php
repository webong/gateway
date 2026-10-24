<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript\Tests\Fixtures;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\HookScript\HookScriptLanguage;
use Webong\Gateway\Protocols\Automation;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\RoutePlan;

final class HookScriptPlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        return RoutePlan::automate(
            new Automation(
                language: HookScriptLanguage::ID,
                source: <<<'PRIMI'
                    forward('https://subscriber.example.test/hook', var('request.content'), 'POST');
                    respond('scripted', 202, ['X-Engine: Primi']);
                    PRIMI,
            ),
            metadata: ['planner' => 'fixture'],
        );
    }
}
