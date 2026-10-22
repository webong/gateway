<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;

/** One bounded HTTPS request to a registered, public credential source. */
final readonly class HttpApplicationMailboxGrantSource implements ApplicationMailboxGrantSource
{
    public function __construct(private MailboxCredentialSourceStore $sources) {}

    public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
    {
        $source = $this->sources->active($account, $sourceId);
        if ($source === null || ! MailboxCredentialSourceStore::safeUrl($source['url'])) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
        $host = parse_url($source['url'], PHP_URL_HOST);
        $ips = @gethostbynamel($host) ?: [];
        foreach (@dns_get_record($host, DNS_AAAA) ?: [] as $record) {
            if (is_string($record['ipv6'] ?? null)) { $ips[] = $record['ipv6']; }
        }
        $ips = array_values(array_unique($ips));
        if ($ips === [] || count($ips) > 16) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
        foreach ($ips as $ip) {
            if (! MailNetworkPolicy::permits($ip, 'public')) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
        }
        $body = json_encode(['account_id' => $account, 'connection_id' => $connection,
            'protocol' => $protocol, 'address' => $address], JSON_THROW_ON_ERROR);
        $curl = curl_init($source['url']);
        if ($curl === false) { throw new RuntimeException('Mailbox credential source is unavailable.'); }
        $response = '';
        try {
            curl_setopt_array($curl, [
                CURLOPT_POST => true, CURLOPT_POSTFIELDS => $body,
                CURLOPT_WRITEFUNCTION => static function (mixed $handle, string $chunk) use (&$response): int {
                    if (strlen($response) + strlen($chunk) > 16384) { return 0; }
                    $response .= $chunk;
                    return strlen($chunk);
                },
                CURLOPT_HTTPHEADER => ['Authorization: Bearer '.$source['authorization'],
                    'Content-Type: application/json', 'Accept: application/json'],
                CURLOPT_RESOLVE => [$host.':443:'.(str_contains($ips[0], ':') ? '['.$ips[0].']' : $ips[0])],
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROXY => '', CURLOPT_NOPROXY => '*',
                CURLOPT_CONNECTTIMEOUT => 2, CURLOPT_TIMEOUT => 4,
                CURLOPT_SSL_VERIFYPEER => true, CURLOPT_SSL_VERIFYHOST => 2,
                CURLOPT_MAXREDIRS => 0, CURLOPT_MAXFILESIZE => 16384,
            ]);
            $result = curl_exec($curl);
            $status = curl_getinfo($curl, CURLINFO_RESPONSE_CODE);
            if ($result !== true || $status !== 200) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            $grant = json_decode($response, true, 16, JSON_THROW_ON_ERROR);
            if (! is_array($grant)) { throw new RuntimeException('Mailbox credential source is unavailable.'); }
            return $grant;
        } catch (\JsonException) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        } finally { curl_close($curl); }
    }
}
