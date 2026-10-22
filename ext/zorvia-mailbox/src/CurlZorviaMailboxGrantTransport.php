<?php

declare(strict_types=1);

namespace Webong\Gateway\ZorviaMailbox;

use RuntimeException;
use Webong\Gateway\Email\MailNetworkPolicy;
use Webong\Gateway\Email\MailboxCredentialSourceStore;

/** Bounded HTTPS transport for the Zorvia-specific request contract. */
final readonly class CurlZorviaMailboxGrantTransport implements ZorviaMailboxGrantTransport
{
    public function exchange(string $url, string $method, array $headers, string $body): array
    {
        if (! MailboxCredentialSourceStore::safeUrl($url) || ! in_array($method, ['PATCH', 'PUT'], true)) {
            throw new RuntimeException('Zorvia mailbox credential source is unavailable.');
        }
        $host = parse_url($url, PHP_URL_HOST);
        $ips = @gethostbynamel($host) ?: [];
        foreach (@dns_get_record($host, DNS_AAAA) ?: [] as $record) {
            if (is_string($record['ipv6'] ?? null)) { $ips[] = $record['ipv6']; }
        }
        $ips = array_values(array_unique($ips));
        if ($ips === [] || count($ips) > 16) {
            throw new RuntimeException('Zorvia mailbox credential source is unavailable.');
        }
        foreach ($ips as $ip) {
            if (! MailNetworkPolicy::permits($ip, 'public')) {
                throw new RuntimeException('Zorvia mailbox credential source is unavailable.');
            }
        }

        $curl = curl_init($url);
        if ($curl === false) { throw new RuntimeException('Zorvia mailbox credential source is unavailable.'); }
        $response = '';
        try {
            curl_setopt_array($curl, [
                CURLOPT_CUSTOMREQUEST => $method, CURLOPT_POSTFIELDS => $body,
                CURLOPT_WRITEFUNCTION => static function (mixed $handle, string $chunk) use (&$response): int {
                    if (strlen($response) + strlen($chunk) > 16384) { return 0; }
                    $response .= $chunk;
                    return strlen($chunk);
                },
                CURLOPT_HTTPHEADER => $headers,
                CURLOPT_RESOLVE => [$host.':443:'.(str_contains($ips[0], ':') ? '['.$ips[0].']' : $ips[0])],
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROXY => '', CURLOPT_NOPROXY => '*',
                CURLOPT_CONNECTTIMEOUT => 2, CURLOPT_TIMEOUT => 4,
                CURLOPT_SSL_VERIFYPEER => true, CURLOPT_SSL_VERIFYHOST => 2,
                CURLOPT_MAXREDIRS => 0, CURLOPT_MAXFILESIZE => 16384,
            ]);
            if (curl_exec($curl) !== true) {
                throw new RuntimeException('Zorvia mailbox credential source is unavailable.');
            }

            return ['status' => curl_getinfo($curl, CURLINFO_RESPONSE_CODE), 'body' => $response];
        } finally {
            curl_close($curl);
        }
    }
}
