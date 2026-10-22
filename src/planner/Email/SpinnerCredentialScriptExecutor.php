<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Throwable;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\Protocol\Delivery;

/** Executes a Go-hosted language without exposing the credential-source bearer. */
final readonly class SpinnerCredentialScriptExecutor implements CredentialScriptExecutor
{
    public function __construct(private DeliveryAdminClient $admin, private string $language) {}

    public function execute(string $source, array $event): Delivery
    {
        try {
            $response = $this->admin->postJson('/automation/credential-hook', [
                'language' => $this->language,
                'source' => $source,
                'event' => $event,
            ]);
            $delivery = $response->successful() ? $response->json('delivery') : null;
            if (! is_array($delivery)) {
                throw new RuntimeException('Credential script execution failed.');
            }

            return new Delivery(
                url: is_string($delivery['url'] ?? null) ? $delivery['url'] : '',
                method: strtoupper(is_string($delivery['method'] ?? null) ? $delivery['method'] : 'POST'),
                headers: $this->headers($delivery['headers'] ?? []),
                body: is_string($delivery['body'] ?? null) ? $delivery['body'] : '',
            );
        } catch (Throwable) {
            throw new RuntimeException('Credential script execution failed.');
        }
    }

    /** @return array<string, list<string>> */
    private function headers(mixed $input): array
    {
        if (! is_array($input)) {
            throw new RuntimeException('Credential script returned invalid headers.');
        }
        $headers = [];
        foreach ($input as $name => $values) {
            if (! is_string($name) || ! is_array($values) || count($values) > 16) {
                throw new RuntimeException('Credential script returned invalid headers.');
            }
            $headers[$name] = array_map(static function (mixed $value): string {
                if (! is_string($value)) {
                    throw new RuntimeException('Credential script returned invalid headers.');
                }
                return $value;
            }, array_values($values));
        }

        return $headers;
    }
}
