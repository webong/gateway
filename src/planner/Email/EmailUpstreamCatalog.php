<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Validated mailbox transport endpoints in the shared configuration collection. */
final readonly class EmailUpstreamCatalog
{
    private const KIND = 'email.upstream';

    public function __construct(private PlannerResourceStore $resources) {}

    /** @return list<stdClass> */
    public function all(): array
    {
        return $this->resources->all(self::KIND);
    }

    public function find(string $id): ?stdClass
    {
        return $this->resources->find(self::KIND, $id);
    }

    /** @return array{address: string, server_name: string, tls_mode?: string}|null */
    public function active(string $protocol, string $name): ?array
    {
        $row = $this->resources->byKey(self::KIND, $protocol.':'.$name);

        return $row !== null && $row->enabled
            ? $row->document : null;
    }

    public function create(array $input): stdClass
    {
        $this->validate($input);

        return $this->resources->create(self::KIND, $input['protocol'].':'.$input['name'],
            $this->document($input), $input['enabled'] ?? true);
    }

    public function replace(string $id, array $input, int $revision): ?stdClass
    {
        $this->validate($input);

        return $this->resources->replace(self::KIND, $id, $input['protocol'].':'.$input['name'],
            $this->document($input), $input['enabled'] ?? true, $revision);
    }

    public function delete(string $id, int $revision): bool
    {
        return $this->resources->delete(self::KIND, $id, $revision);
    }

    private function document(array $input): array
    {
        return ['address' => $input['address'], 'server_name' => $input['server_name'],
            'network_scope' => $input['network_scope'] ?? 'public',
            ...($input['protocol'] === 'smtp' ? ['tls_mode' => $input['tls_mode']] : [])];
    }

    private function validate(array $input): void
    {
        if (array_diff(array_keys($input), ['protocol', 'name', 'address', 'server_name', 'network_scope', 'tls_mode', 'enabled']) !== [] ||
            ! in_array($input['protocol'] ?? null, ['imap', 'pop3', 'smtp'], true) ||
            ! is_string($input['name'] ?? null) || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1 ||
            ! is_string($input['address'] ?? null) || strlen($input['address']) > 320 ||
            ! is_string($input['server_name'] ?? null) || strlen($input['server_name']) > 253 ||
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $input['server_name']) !== 1 ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled'])) ||
            ($input['protocol'] === 'smtp' && ! in_array($input['tls_mode'] ?? null, ['implicit', 'starttls'], true)) ||
            ($input['protocol'] !== 'smtp' && array_key_exists('tls_mode', $input)) ||
            ! in_array($input['network_scope'] ?? 'public', ['public', 'private'], true) ||
            ! $this->validAddress($input['address'])) {
            throw new InvalidArgumentException('Email upstream is invalid.');
        }
    }

    private function validAddress(string $address): bool
    {
        $parts = parse_url('tcp://'.$address);
        if (! is_array($parts) || ! is_string($parts['host'] ?? null) ||
            ! is_int($parts['port'] ?? null) || $parts['port'] < 1 || $parts['port'] > 65535 ||
            isset($parts['user']) || isset($parts['pass']) || isset($parts['path']) || isset($parts['query']) ||
            isset($parts['fragment']) || $address !== $parts['host'].':'.$parts['port']) {
            return false;
        }
        return preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $parts['host']) === 1;
    }
}
