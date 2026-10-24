<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use DateTimeImmutable;
use InvalidArgumentException;
use JsonException;

final readonly class CompanionActionInvocation
{
    public const string VERSION = 'v1';

    /** @param array<string, mixed> $input */
    public function __construct(
        public string $action,
        public string $executionId,
        public string $idempotencyKey,
        public array $input,
        public DateTimeImmutable $deadline,
        public ?string $companionVersion = null,
        public ?string $companionDigest = null,
        public string $protocolVersion = self::VERSION,
    ) {
        new CompanionActionCapability($action, $companionVersion, $companionDigest);
        if ($protocolVersion !== self::VERSION) {
            throw new InvalidArgumentException("Unsupported companion action protocol version [{$protocolVersion}].");
        }
        if (trim($executionId) === '' || strlen($executionId) > 128) {
            throw new InvalidArgumentException('Gateway companion action execution ID is required and cannot exceed 128 bytes.');
        }
        if (trim($idempotencyKey) === '' || strlen($idempotencyKey) > 255) {
            throw new InvalidArgumentException('Gateway companion action idempotency key is required and cannot exceed 255 bytes.');
        }
        if ($deadline <= new DateTimeImmutable('now')) {
            throw new InvalidArgumentException('Gateway companion action deadline has expired.');
        }
    }

    /** @param array<string, mixed> $attributes */
    public static function fromArray(array $attributes): self
    {
        $allowed = [
            'protocol_version', 'action', 'companion_version', 'companion_digest',
            'execution_id', 'idempotency_key', 'deadline', 'input',
        ];
        if (array_diff(array_keys($attributes), $allowed) !== []) {
            throw new InvalidArgumentException('Gateway companion action invocation contains unsupported fields.');
        }
        $input = $attributes['input'] ?? null;
        if (! is_array($input) || array_is_list($input) && $input !== []) {
            throw new InvalidArgumentException('Gateway companion action input must be an object.');
        }
        $deadline = $attributes['deadline'] ?? null;
        if (! is_string($deadline) || trim($deadline) === '') {
            throw new InvalidArgumentException('Gateway companion action deadline must be an RFC 3339 timestamp.');
        }
        try {
            $parsedDeadline = new DateTimeImmutable($deadline);
        } catch (\Throwable) {
            throw new InvalidArgumentException('Gateway companion action deadline must be an RFC 3339 timestamp.');
        }

        foreach (['action', 'execution_id', 'idempotency_key', 'protocol_version'] as $field) {
            if (! is_string($attributes[$field] ?? null)) {
                throw new InvalidArgumentException("Gateway companion action {$field} must be a string.");
            }
        }
        foreach (['companion_version', 'companion_digest'] as $field) {
            if (isset($attributes[$field]) && ! is_string($attributes[$field])) {
                throw new InvalidArgumentException("Gateway companion action {$field} must be a string.");
            }
        }

        return new self(
            action: $attributes['action'],
            executionId: $attributes['execution_id'],
            idempotencyKey: $attributes['idempotency_key'],
            input: $input,
            companionVersion: $attributes['companion_version'] ?? null,
            companionDigest: $attributes['companion_digest'] ?? null,
            deadline: $parsedDeadline,
            protocolVersion: $attributes['protocol_version'],
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'protocol_version' => $this->protocolVersion,
            'action' => $this->action,
            'companion_version' => $this->companionVersion,
            'companion_digest' => $this->companionDigest,
            'execution_id' => $this->executionId,
            'idempotency_key' => $this->idempotencyKey,
            'deadline' => $this->deadline->format(DATE_RFC3339_EXTENDED),
            'input' => $this->input,
        ], static fn (mixed $value): bool => $value !== null);
    }

    /** @throws JsonException */
    public function fingerprint(): string
    {
        $document = [
            'action' => $this->action,
            'companion_version' => $this->companionVersion,
            'companion_digest' => $this->companionDigest,
            'input' => self::canonicalize($this->input),
        ];

        return hash('sha256', json_encode($document, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES));
    }

    public function withExecutionId(string $executionId): self
    {
        return new self(
            action: $this->action,
            executionId: $executionId,
            idempotencyKey: $this->idempotencyKey,
            input: $this->input,
            deadline: $this->deadline,
            companionVersion: $this->companionVersion,
            companionDigest: $this->companionDigest,
            protocolVersion: $this->protocolVersion,
        );
    }

    private static function canonicalize(mixed $value): mixed
    {
        if (! is_array($value)) {
            return $value;
        }
        if (! array_is_list($value)) {
            ksort($value);
        }
        foreach ($value as $key => $nested) {
            $value[$key] = self::canonicalize($nested);
        }

        return $value;
    }
}
