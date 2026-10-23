<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;

/**
 * Gateway's bounded, opaque SRS return token. Original sender addresses are
 * stored separately so long local parts and repeated forwarding cannot create
 * invalid SMTP addresses. This is not a general SRS0 address decoder.
 */
final readonly class SrsCodec
{
    /** @param non-empty-list<string> $keys */
    public function __construct(
        public string $domain,
        private array $keys,
        private int $maxAgeSeconds = 21 * 86400,
    ) {
        if (strlen($domain) > 200 || ! str_contains($domain, '.') || filter_var($domain, FILTER_VALIDATE_DOMAIN, FILTER_FLAG_HOSTNAME) === false) {
            throw new InvalidArgumentException('A valid SRS return domain is required.');
        }
        if ($keys === [] || ! array_is_list($keys) || array_filter($keys, static fn (mixed $key): bool => ! is_string($key) || strlen($key) < 32) !== []) {
            throw new InvalidArgumentException('SRS requires one or more persistent keys of at least 32 bytes.');
        }
        if ($maxAgeSeconds < 3600 || $maxAgeSeconds > 90 * 86400) {
            throw new InvalidArgumentException('SRS return lifetime must be between one hour and 90 days.');
        }
    }

    public function issue(?int $at = null): SrsReturnToken
    {
        $timestamp = $at ?? time();
        if ($timestamp < 1 || $timestamp > 0xffffffff) {
            throw new InvalidArgumentException('SRS token timestamp is outside the supported range.');
        }
        $stamp = sprintf('%08x', $timestamp);
        $nonce = self::base64Url(random_bytes(12));
        $mac = self::base64Url(substr($this->mac($stamp, $nonce, $this->keys[0]), 0, 16));
        $local = 'SRS0-'.$stamp.'-'.$nonce.'-'.$mac;
        $address = $local.'@'.$this->domain;

        return new SrsReturnToken($address, hash('sha256', $address), $timestamp + $this->maxAgeSeconds);
    }

    public function verify(string $address, ?int $at = null): ?SrsReturnToken
    {
        $parts = explode('@', $address);
        if (count($parts) !== 2 || strcasecmp($parts[1], $this->domain) !== 0
            || ! preg_match('/^SRS0-([0-9a-f]{8})-([A-Za-z0-9_-]{16})-([A-Za-z0-9_-]{22})$/D', $parts[0], $matches)) {
            return null;
        }
        $timestamp = hexdec($matches[1]);
        $now = $at ?? time();
        if ($timestamp > $now + 300 || $timestamp + $this->maxAgeSeconds < $now) {
            return null;
        }
        $valid = false;
        foreach ($this->keys as $key) {
            $expected = self::base64Url(substr($this->mac($matches[1], $matches[2], $key), 0, 16));
            $valid = hash_equals($expected, $matches[3]) || $valid;
        }
        if (! $valid) {
            return null;
        }

        $canonical = $parts[0].'@'.$this->domain;

        return new SrsReturnToken($canonical, hash('sha256', $canonical), $timestamp + $this->maxAgeSeconds);
    }

    private function mac(string $stamp, string $nonce, string $key): string
    {
        return hash_hmac('sha256', "gateway-srs-v1\0".strtolower($this->domain)."\0".$stamp."\0".$nonce, $key, true);
    }

    private static function base64Url(string $bytes): string
    {
        return rtrim(strtr(base64_encode($bytes), '+/', '-_'), '=');
    }
}
