<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Illuminate\Http\Client\Factory as HttpFactory;
use RuntimeException;
use Throwable;
use Webong\Gateway\Companion\ExtensionSecretStore;

final readonly class CloudflareDnsProvisioner implements DnsProvisioner
{
    private const string EXTENSION = 'webong/dns';

    public function __construct(private HttpFactory $http, private ExtensionSecretStore $secrets) {}

    public function provision(array $zone, array $options = [], array $state = []): array
    {
        $reference = $this->credentialReference($options['credential_ref'] ?? null);
        $token = $this->token($reference);

        $parent = $this->parentZone($zone['name'], $token, $options);
        $previousRecords = is_array($state['managed_record_ids'] ?? null) ? $state['managed_record_ids'] : [];
        $previouslyManaged = array_fill_keys(array_values(array_filter(
            $previousRecords,
            static fn (mixed $record): bool => is_string($record) && $record !== '',
        )), true);
        $managed = [];
        try {
            foreach ($zone['nameservers'] as $nameserver) {
                $existing = $this->request($token, 'get', "/zones/{$parent['id']}/dns_records", [
                    'type' => 'NS', 'name' => $zone['name'], 'content' => $nameserver,
                ]);
                if (is_string($existing[0]['id'] ?? null)) {
                    if (isset($previouslyManaged[$existing[0]['id']])) {
                        $managed[] = $existing[0]['id'];
                    }
                    continue;
                }
                $record = $this->request($token, 'post', "/zones/{$parent['id']}/dns_records", [
                    'type' => 'NS',
                    'name' => $zone['name'],
                    'content' => $nameserver,
                    'ttl' => max(60, $zone['ttl']),
                    'proxied' => false,
                ]);
                if (! is_string($record['id'] ?? null)) {
                    throw new RuntimeException('Cloudflare did not return the created NS record.');
                }
                $managed[] = $record['id'];
            }
        } catch (Throwable $exception) {
            foreach ($managed as $record) {
                try {
                    $this->request($token, 'delete', "/zones/{$parent['id']}/dns_records/{$record}");
                } catch (Throwable) {
                    // Preserve the original provisioning failure after best-effort compensation.
                }
            }
            throw $exception;
        }

        return [
            'credential_ref' => $reference,
            'parent_zone_id' => $parent['id'],
            'managed_record_ids' => array_values(array_unique($managed)),
        ];
    }

    public function deprovision(array $zone, array $state = []): void
    {
        $parent = $state['parent_zone_id'] ?? null;
        $records = $state['managed_record_ids'] ?? [];
        if (! is_string($parent) || ! is_array($records) || $records === []) {
            return;
        }
        $reference = $this->credentialReference($state['credential_ref'] ?? null);
        $token = $this->token($reference);
        foreach ($records as $record) {
            if (is_string($record) && $record !== '') {
                $this->request($token, 'delete', "/zones/{$parent}/dns_records/{$record}");
            }
        }
    }

    private function credentialReference(mixed $reference): string
    {
        if (! is_string($reference) || preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D', $reference) !== 1) {
            throw new RuntimeException('Cloudflare DNS provisioning requires a valid credential_ref.');
        }

        return $reference;
    }

    private function token(string $reference): string
    {
        $secret = $this->secrets->resolve(self::EXTENSION, $reference);
        $value = $secret['value'] ?? null;
        if (! is_array($value) || array_keys($value) !== ['api_token']
            || ! is_string($value['api_token']) || trim($value['api_token']) === '') {
            throw new RuntimeException('Cloudflare DNS credential is unavailable.');
        }

        return trim($value['api_token']);
    }

    /** @param array<string, mixed> $options
     *  @return array{id: string, name: string}
     */
    private function parentZone(string $zone, string $token, array $options): array
    {
        if (is_string($options['parent_zone_id'] ?? null) && $options['parent_zone_id'] !== '') {
            return ['id' => $options['parent_zone_id'], 'name' => (string) ($options['parent_zone'] ?? '')];
        }

        $labels = explode('.', $zone);
        array_shift($labels);
        while (count($labels) >= 2) {
            $candidate = implode('.', $labels);
            $zones = $this->request($token, 'get', '/zones', ['name' => $candidate, 'status' => 'active']);
            if (is_string($zones[0]['id'] ?? null)) {
                return ['id' => $zones[0]['id'], 'name' => $candidate];
            }
            array_shift($labels);
        }

        throw new RuntimeException("Cloudflare does not manage a parent zone for {$zone}.");
    }

    /** @param array<string, mixed> $payload */
    private function request(string $token, string $method, string $path, array $payload = []): mixed
    {
        $base = rtrim((string) config('gateway.dns.provisioners.cloudflare.base_url', 'https://api.cloudflare.com/client/v4'), '/');
        $request = $this->http->withToken($token)->acceptJson()->timeout(10);
        $response = $method === 'get'
            ? $request->get($base.$path, $payload)
            : $request->{$method}($base.$path, $payload);
        if (! $response->successful() || $response->json('success') !== true) {
            $message = $response->json('errors.0.message');
            throw new RuntimeException(is_string($message) ? "Cloudflare DNS provisioning failed: {$message}" : 'Cloudflare DNS provisioning failed.');
        }

        return $response->json('result');
    }
}
