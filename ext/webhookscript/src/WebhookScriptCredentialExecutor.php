<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use RuntimeException;
use Webong\Gateway\Email\CredentialScriptExecutor;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\IngressRequest;

final readonly class WebhookScriptCredentialExecutor implements CredentialScriptExecutor
{
    public function __construct(private WebhookScriptRuntime $runtime) {}

    public function execute(string $source, array $event): Delivery
    {
        $request = new IngressRequest(
            deliveryId: 'credential-hook', method: 'POST', host: 'gateway.internal',
            path: '/automation/credential-hook', rawQuery: '', headers: [],
            body: json_encode($event, JSON_THROW_ON_ERROR), event: 'credential',
        );
        $variables = [];
        foreach ($event as $name => $value) {
            $variables['credential.'.$name] = $value;
        }
        $outcome = $this->runtime->execute($source, $request, $variables);
        if (count($outcome->deliveries) !== 1 || $outcome->response !== null || $outcome->schedules !== []) {
            throw new RuntimeException('Credential hook must return exactly one request.');
        }

        return $outcome->deliveries[0];
    }
}
