<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Webong\Gateway\Protocol\Delivery;

/** Executes one bounded request against the source's registered public HTTPS origin. */
final class MailboxGrantHttpClient
{
    /** @param array{url: string, authorization: string} $source
     * @return array<string, mixed>
     */
    public function request(array $source, Delivery $delivery): array
    {
        if (! MailboxCredentialSourceStore::safeUrl($source['url']) || $delivery->url !== $source['url'] ||
            ! in_array($delivery->method ?: 'POST', ['POST', 'PUT', 'PATCH'], true) || strlen($delivery->body) > 65536) {
            throw new RuntimeException('Mailbox credential source is unavailable.');
        }
        $headers = [];
        foreach ($delivery->headers as $name => $values) {
            if (preg_match("/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/D", $name) !== 1 ||
                in_array(strtolower($name), ['authorization', 'proxy-authorization', 'cookie', 'host',
                    'connection', 'content-length', 'transfer-encoding', 'expect'], true) ||
                count($values) > 16) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            foreach ($values as $value) {
                if (preg_match('/[\r\n]/', $name.$value) === 1 || strlen($name) > 128 || strlen($value) > 4096) {
                    throw new RuntimeException('Mailbox credential source is unavailable.');
                }
                $headers[] = $name.': '.$value;
            }
        }
        $headers[] = 'Authorization: Bearer '.$source['authorization'];
        $headers[] = 'Accept: application/json';

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
        $curl = curl_init($source['url']);
        if ($curl === false) { throw new RuntimeException('Mailbox credential source is unavailable.'); }
        $response = '';
        try {
            curl_setopt_array($curl, [
                CURLOPT_CUSTOMREQUEST => $delivery->method ?: 'POST', CURLOPT_POSTFIELDS => $delivery->body,
                CURLOPT_WRITEFUNCTION => static function (mixed $handle, string $chunk) use (&$response): int {
                    if (strlen($response) + strlen($chunk) > 16384) { return 0; }
                    $response .= $chunk;
                    return strlen($chunk);
                },
                CURLOPT_HTTPHEADER => $headers,
                CURLOPT_RESOLVE => [$host.':443:'.(str_contains($ips[0], ':') ? '['.$ips[0].']' : $ips[0])],
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROXY => '', CURLOPT_NOPROXY => '*', CURLOPT_CONNECTTIMEOUT => 2, CURLOPT_TIMEOUT => 4,
                CURLOPT_SSL_VERIFYPEER => true, CURLOPT_SSL_VERIFYHOST => 2, CURLOPT_MAXREDIRS => 0,
                CURLOPT_MAXFILESIZE => 16384,
            ]);
            $result = curl_exec($curl);
            if ($result !== true || curl_getinfo($curl, CURLINFO_RESPONSE_CODE) !== 200) {
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
