<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;
use JsonException;

final readonly class ScheduleCommand
{
    /** @param array<string, mixed> $payload */
    public function __construct(
        public string $key,
        public string $name,
        public string $cron,
        public string $language,
        public string $source,
        public string $timezone = 'UTC',
        public array $payload = [],
        public bool $enabled = true,
    ) {
        foreach (['key' => $key, 'name' => $name, 'cron' => $cron, 'timezone' => $timezone, 'language' => $language, 'source' => $source] as $field => $value) {
            if (trim($value) === '') {
                throw new InvalidArgumentException("Schedule {$field} is required.");
            }
        }
        if (strlen($key) > 255 || preg_match('/^[A-Za-z0-9_.:-]+$/', $key) !== 1) {
            throw new InvalidArgumentException('Schedule key must contain only letters, numbers, dots, colons, underscores, and dashes, up to 255 characters.');
        }
        if (strlen($name) > 255 || strlen($cron) > 100 || strlen($timezone) > 100 || strlen($language) > 64
            || preg_match('/^[a-z][a-z0-9_-]*$/', $language) !== 1 || strlen($source) > 65535) {
            throw new InvalidArgumentException('Schedule command exceeds its field limits.');
        }
        try {
            $payloadBytes = strlen(json_encode($payload, JSON_THROW_ON_ERROR));
        } catch (JsonException $exception) {
            throw new InvalidArgumentException('Schedule payload must be JSON serializable.', previous: $exception);
        }
        if ($payloadBytes > 65535) {
            throw new InvalidArgumentException('Schedule payload exceeds 65535 bytes.');
        }
    }

    /** @param array<string, mixed> $payload */
    public static function fromArray(array $payload): self
    {
        $unknown = array_diff(array_keys($payload), [
            'key', 'name', 'cron', 'timezone', 'language', 'source', 'payload', 'enabled',
        ]);
        if ($unknown !== []) {
            throw new InvalidArgumentException('Unknown schedule fields: '.implode(', ', $unknown).'.');
        }
        $schedulePayload = $payload['payload'] ?? [];
        if (! is_array($schedulePayload)) {
            throw new InvalidArgumentException('Schedule payload must be an object.');
        }
        if (array_key_exists('enabled', $payload) && ! is_bool($payload['enabled'])) {
            throw new InvalidArgumentException('Schedule enabled must be a boolean.');
        }

        return new self(
            key: is_string($payload['key'] ?? null) ? $payload['key'] : '',
            name: is_string($payload['name'] ?? null) ? $payload['name'] : '',
            cron: is_string($payload['cron'] ?? null) ? $payload['cron'] : '',
            language: is_string($payload['language'] ?? null) ? $payload['language'] : '',
            source: is_string($payload['source'] ?? null) ? $payload['source'] : '',
            timezone: is_string($payload['timezone'] ?? null) ? $payload['timezone'] : 'UTC',
            payload: $schedulePayload,
            enabled: $payload['enabled'] ?? true,
        );
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return [
            'key' => $this->key,
            'name' => $this->name,
            'cron' => $this->cron,
            'timezone' => $this->timezone,
            'language' => $this->language,
            'source' => $this->source,
            'payload' => $this->payload,
            'enabled' => $this->enabled,
        ];
    }
}
