<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocols;

use InvalidArgumentException;

final readonly class CompanionActionResult
{
    public const string SUCCEEDED = 'succeeded';
    public const string FAILED = 'failed';
    public const string IN_PROGRESS = 'in_progress';

    /**
     * @param array<string, mixed> $output
     * @param array{code: string, message: string, retryable: bool}|null $error
     */
    public function __construct(
        public string $executionId,
        public string $status,
        public array $output = [],
        public ?array $error = null,
        public string $protocolVersion = CompanionActionInvocation::VERSION,
    ) {
        if ($protocolVersion !== CompanionActionInvocation::VERSION) {
            throw new InvalidArgumentException("Unsupported companion action protocol version [{$protocolVersion}].");
        }
        if (trim($executionId) === '') {
            throw new InvalidArgumentException('Gateway companion action result requires an execution ID.');
        }
        if (! in_array($status, [self::SUCCEEDED, self::FAILED, self::IN_PROGRESS], true)) {
            throw new InvalidArgumentException("Invalid companion action result status [{$status}].");
        }
        if ($status === self::FAILED) {
            if (! is_string($error['code'] ?? null) || trim($error['code']) === ''
                || ! is_string($error['message'] ?? null) || trim($error['message']) === ''
                || ! is_bool($error['retryable'] ?? null)) {
                throw new InvalidArgumentException('Failed companion action result requires a structured error.');
            }
        } elseif ($error !== null) {
            throw new InvalidArgumentException('Only failed companion action results may contain an error.');
        }
    }

    /** @param array<string, mixed> $output */
    public static function succeeded(string $executionId, array $output = []): self
    {
        return new self($executionId, self::SUCCEEDED, $output);
    }

    public static function failed(string $executionId, string $code, string $message, bool $retryable = false): self
    {
        return new self($executionId, self::FAILED, error: [
            'code' => $code,
            'message' => $message,
            'retryable' => $retryable,
        ]);
    }

    public static function inProgress(string $executionId): self
    {
        return new self($executionId, self::IN_PROGRESS);
    }

    /** @param array<string, mixed> $attributes */
    public static function fromArray(array $attributes): self
    {
        return new self(
            executionId: (string) ($attributes['execution_id'] ?? ''),
            status: (string) ($attributes['status'] ?? ''),
            output: is_array($attributes['output'] ?? null) ? $attributes['output'] : [],
            error: is_array($attributes['error'] ?? null) ? $attributes['error'] : null,
            protocolVersion: (string) ($attributes['protocol_version'] ?? ''),
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'protocol_version' => $this->protocolVersion,
            'execution_id' => $this->executionId,
            'status' => $this->status,
            'output' => $this->output,
            'error' => $this->error,
        ], static fn (mixed $value): bool => $value !== null);
    }
}
