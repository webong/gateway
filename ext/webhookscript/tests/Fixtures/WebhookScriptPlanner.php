<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript\Tests\Fixtures;

use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Automation;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;

final class WebhookScriptPlanner implements RoutePlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        return RoutePlan::automate(
            new Automation(
                language: Automation::WEBHOOKSCRIPT,
                source: <<<'PRIMI'
                    forward('https://subscriber.example.test/hook', var('request.content'), 'POST');
                    respond('scripted', 202, ['X-Engine: Primi']);
                    PRIMI,
            ),
            metadata: ['planner' => 'fixture'],
        );
    }
}
