<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;

/** Credential-only probes; SMTP tests never send MAIL FROM or DATA. */
class MailboxConnectionTester
{
    public function __construct(private readonly MailboxConnectionStore $connections,
        private readonly EmailUpstreamCatalog $upstreams) {}

    public function test(string $id): bool
    {
        $imap = $this->connections->resolve($id, 'imap', allowDisabled: true);
        $smtp = $this->connections->resolve($id, 'smtp', allowDisabled: true);
        if (($imap['mode'] ?? null) === 'imap') { return $this->testIMAP($imap); }
        if ($smtp === null) { return false; }
        if (($smtp['mode'] ?? 'mailbox') === 'sending') { return $this->testSMTP($smtp); }
        if ($imap === null) { return false; }

        return $this->testIMAP($imap) && $this->testSMTP($smtp);
    }

    /** @param array<string, string> $credentials */
    private function testIMAP(array $credentials): bool
    {
        $upstream = $this->upstreams->active('imap', $credentials['upstream_id']);
        if (! is_array($upstream)) { return false; }
        $stream = $this->connect($upstream, true);
        if (! is_resource($stream)) { return false; }
        try {
            if (! str_starts_with($this->line($stream), '* OK')) { return false; }
            if ($credentials['mechanism'] === 'XOAUTH2') {
                $initial = base64_encode('user='.$credentials['username']."\x01auth=Bearer ".$credentials['secret']."\x01\x01");
                fwrite($stream, "GATEWAY1 AUTHENTICATE XOAUTH2 {$initial}\r\n");
            } else {
                $username = $this->quote($credentials['username']);
                $secret = $this->quote($credentials['secret']);
                fwrite($stream, "GATEWAY1 LOGIN {$username} {$secret}\r\n");
            }
            for ($i = 0; $i < 32; $i++) {
                $line = $this->line($stream);
                if (str_starts_with($line, 'GATEWAY1 ')) { return str_starts_with($line, 'GATEWAY1 OK'); }
                if (! str_starts_with($line, '* ')) { return false; }
            }
            return false;
        } finally { fclose($stream); }
    }

    /** @param array<string, string> $credentials */
    private function testSMTP(array $credentials): bool
    {
        $upstream = $this->upstreams->active('smtp', $credentials['upstream_id']);
        if (! is_array($upstream) || ! in_array($upstream['tls_mode'] ?? null, ['implicit', 'starttls'], true)) { return false; }
        $stream = $this->connect($upstream, $upstream['tls_mode'] === 'implicit');
        if (! is_resource($stream)) { return false; }
        try {
            if ($this->smtpReply($stream) !== 220) { return false; }
            fwrite($stream, "EHLO gateway.local\r\n");
            if ($this->smtpReply($stream) !== 250) { return false; }
            if ($upstream['tls_mode'] === 'starttls') {
                fwrite($stream, "STARTTLS\r\n");
                if ($this->smtpReply($stream) !== 220 || @stream_socket_enable_crypto($stream, true, STREAM_CRYPTO_METHOD_TLS_CLIENT) !== true) { return false; }
                fwrite($stream, "EHLO gateway.local\r\n");
                if ($this->smtpReply($stream) !== 250) { return false; }
            }
            $initial = $credentials['mechanism'] === 'XOAUTH2'
                ? 'user='.$credentials['username']."\x01auth=Bearer ".$credentials['secret']."\x01\x01"
                : "\x00".$credentials['username']."\x00".$credentials['secret'];
            fwrite($stream, 'AUTH '.($credentials['mechanism'] === 'XOAUTH2' ? 'XOAUTH2' : 'PLAIN').' '.base64_encode($initial)."\r\n");
            $accepted = $this->smtpReply($stream) === 235;
            fwrite($stream, "QUIT\r\n");

            return $accepted;
        } finally { fclose($stream); }
    }

    private function connect(array $upstream, bool $implicitTLS): mixed
    {
        $address = $upstream['address'] ?? null;
        $name = $upstream['server_name'] ?? null;
        $scope = $upstream['network_scope'] ?? 'public';
        if (! is_string($address) || ! is_string($name) || $address === '' || $name === '' ||
            ! in_array($scope, ['public', 'private'], true)) { return false; }
        $parts = parse_url('tcp://'.$address);
        if (! is_array($parts) || ! is_string($parts['host'] ?? null) || ! is_int($parts['port'] ?? null)) { return false; }
        $host = $parts['host'];
        $ips = filter_var($host, FILTER_VALIDATE_IP) !== false ? [$host] : (@gethostbynamel($host) ?: []);
        foreach (@dns_get_record($host, DNS_AAAA) ?: [] as $record) {
            if (is_string($record['ipv6'] ?? null)) { $ips[] = $record['ipv6']; }
        }
        $context = stream_context_create(['ssl' => ['verify_peer' => true, 'verify_peer_name' => true,
            'peer_name' => $name, 'SNI_enabled' => true, 'crypto_method' => STREAM_CRYPTO_METHOD_TLS_CLIENT]]);
        foreach (array_slice(array_unique($ips), 0, 8) as $ip) {
            if (! MailNetworkPolicy::permits($ip, $scope)) { continue; }
            $endpoint = str_contains($ip, ':') ? '['.$ip.']:'.$parts['port'] : $ip.':'.$parts['port'];
            $stream = @stream_socket_client('tcp://'.$endpoint, $errno, $error, 5, STREAM_CLIENT_CONNECT, $context);
            if ($stream === false) { continue; }
            stream_set_timeout($stream, 5);
            if ($implicitTLS && @stream_socket_enable_crypto($stream, true, STREAM_CRYPTO_METHOD_TLS_CLIENT) !== true) {
                fclose($stream);
                continue;
            }
            return $stream;
        }
        return false;
    }

    private function line(mixed $stream): string
    {
        $line = fgets($stream, 4096);
        if ($line === false || ! str_ends_with($line, "\n") || stream_get_meta_data($stream)['timed_out']) {
            throw new RuntimeException('Mailbox test failed.');
        }

        return rtrim($line, "\r\n");
    }

    private function smtpReply(mixed $stream): int
    {
        for ($i = 0; $i < 32; $i++) {
            $line = $this->line($stream);
            if (! preg_match('/^([0-9]{3})([ -])/', $line, $match)) { return 0; }
            if ($match[2] === ' ') { return (int) $match[1]; }
        }

        return 0;
    }

    private function quote(string $value): string
    {
        if ($value === '' || strlen($value) > 4096 || preg_match('/[^\x20-\x7E]/', $value) === 1) {
            throw new RuntimeException('Mailbox credential cannot be encoded for IMAP LOGIN.');
        }

        return '"'.str_replace(['\\', '"'], ['\\\\', '\\"'], $value).'"';
    }
}
