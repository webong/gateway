<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** One active signing selector per author domain; private PEM stays encrypted. */
final readonly class EmailDkimKeyStore
{
    private const KIND = 'email.dkim-key';

    public function __construct(private PlannerResourceStore $resources) {}

    public function all(): array { return $this->resources->all(self::KIND); }
    public function find(string $id): ?stdClass { return $this->resources->find(self::KIND, $id); }

    /** @return array{domain: string, selector: string, private_key_pem: string}|null */
    public function active(string $domain): ?array
    {
        $row = $this->resources->byKey(self::KIND, $domain);

        return $row !== null && $row->enabled ? ['domain' => $row->key, ...$row->document] : null;
    }

    public function ensureGenerated(string $domain, string $selector): stdClass
    {
        $existing = $this->resources->byKey(self::KIND, $domain);
        if ($existing !== null) { return $existing; }
        if (preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/D', $selector) !== 1
            || strlen($selector) > 63) {
            throw new InvalidArgumentException('Invalid managed DKIM selector.');
        }
        $key = @openssl_pkey_new(['private_key_type' => OPENSSL_KEYTYPE_RSA, 'private_key_bits' => 2048]);
        if ($key === false || ! openssl_pkey_export($key, $pem)) {
            throw new InvalidArgumentException('Could not generate a managed DKIM key.');
        }
        return $this->create(['domain' => $domain, 'selector' => $selector, 'private_key_pem' => $pem]);
    }

    /** @return array{name: string, type: string, value: string}|null */
    public function publicRecord(string $domain): ?array
    {
        $key = $this->active($domain);
        if ($key === null) { return null; }
        $private = openssl_pkey_get_private($key['private_key_pem']);
        $details = $private === false ? false : openssl_pkey_get_details($private);
        if ($details === false || ! is_string($details['key'] ?? null)
            || preg_match('/-----BEGIN PUBLIC KEY-----\s*([A-Za-z0-9+\/=\s]+)-----END PUBLIC KEY-----/', $details['key'], $match) !== 1) {
            return null;
        }
        $public = preg_replace('/\s+/', '', $match[1]);
        return ['name' => $key['selector'].'._domainkey.'.$domain, 'type' => 'TXT',
            'value' => 'v=DKIM1; k=rsa; p='.$public];
    }

    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->resources->create(self::KIND, $input['domain'], $this->document($input),
            $input['enabled'] ?? true);
    }

    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        $current = $this->find($id);
        if ($current === null) { return null; }
        $input['private_key_pem'] ??= $current->document['private_key_pem'];
        $this->validate($input);

        return $this->resources->replace(self::KIND, $id, $input['domain'], $this->document($input),
            $input['enabled'] ?? $current->enabled, $revision);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    private function document(array $input): array
    {
        return ['selector' => $input['selector'], 'private_key_pem' => $input['private_key_pem']];
    }

    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['domain', 'selector', 'private_key_pem', 'enabled']) !== [] ||
            ! is_string($input['domain'] ?? null) || strlen($input['domain']) > 253 ||
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$/D', $input['domain']) !== 1 ||
            ! is_string($input['selector'] ?? null) ||
            preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/D', $input['selector']) !== 1 ||
            strlen($input['selector']) > 63 || ! is_string($input['private_key_pem'] ?? null) ||
            strlen($input['private_key_pem']) > 16384 ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid DKIM signing key.');
        }
        $key = openssl_pkey_get_private($input['private_key_pem']);
        $details = $key === false ? false : openssl_pkey_get_details($key);
        $allowedTypes = [OPENSSL_KEYTYPE_RSA];
        if (defined('OPENSSL_KEYTYPE_ED25519')) { $allowedTypes[] = constant('OPENSSL_KEYTYPE_ED25519'); }
        if ($details === false || ! in_array($details['type'], $allowedTypes, true) ||
            ($details['type'] === OPENSSL_KEYTYPE_RSA && $details['bits'] < 2048)) {
            throw new InvalidArgumentException('DKIM signing key must be RSA 2048+ or Ed25519.');
        }
    }
}
