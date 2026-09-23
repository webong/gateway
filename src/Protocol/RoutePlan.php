<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

final readonly class RoutePlan
{
    public const VERSION = 'v1';
    public const RELAY = 'relay';
    public const RESPOND = 'respond';
    public const PASS_THROUGH = 'pass_through';
    public const AUTOMATION = 'automation';

    /** @param list<Delivery> $relays @param array<string, string> $metadata */
    public function __construct(
        public string $action,
        public ?Response $immediateResponse = null,
        public ?Delivery $reply = null,
        public array $relays = [],
        public ?Automation $automation = null,
        public array $metadata = [],
        public string $version = self::VERSION,
    ) {
        $this->assertValid();
    }

    public static function passThrough(array $metadata = []): self
    {
        return new self(self::PASS_THROUGH, metadata: $metadata);
    }

    public static function respond(Response $response, array $metadata = []): self
    {
        return new self(self::RESPOND, immediateResponse: $response, metadata: $metadata);
    }

    /** @param list<Delivery> $relays */
    public static function relay(?Delivery $reply = null, array $relays = [], array $metadata = []): self
    {
        return new self(self::RELAY, reply: $reply, relays: $relays, metadata: $metadata);
    }

    /** @param list<Delivery> $relays */
    public static function automate(Automation $automation, array $relays = [], array $metadata = []): self
    {
        return new self(self::AUTOMATION, relays: $relays, automation: $automation, metadata: $metadata);
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return array_filter([
            'version' => $this->version,
            'action' => $this->action,
            'immediate_response' => $this->immediateResponse?->toArray(),
            'reply' => $this->reply?->toArray(),
            'relays' => array_map(static fn (Delivery $delivery): array => $delivery->toArray(), $this->relays),
            'automation' => $this->automation?->toArray(),
            'metadata' => $this->metadata,
        ], static fn (mixed $value): bool => $value !== null && $value !== []);
    }

    private function assertValid(): void
    {
        if ($this->version !== self::VERSION) {
            throw new InvalidArgumentException('Unsupported gateway protocol version.');
        }

        if (! in_array($this->action, [self::RELAY, self::RESPOND, self::PASS_THROUGH, self::AUTOMATION], true)) {
            throw new InvalidArgumentException('Unsupported gateway route action.');
        }

        if ($this->action === self::PASS_THROUGH
            && ($this->immediateResponse !== null || $this->reply !== null || $this->relays !== [] || $this->automation !== null)) {
            throw new InvalidArgumentException('Pass-through plans cannot contain deliveries.');
        }

        if ($this->action === self::RESPOND
            && ($this->immediateResponse === null || $this->reply !== null || $this->relays !== [] || $this->automation !== null)) {
            throw new InvalidArgumentException('Respond plans require only an immediate response.');
        }

        if ($this->action === self::RELAY && ($this->immediateResponse !== null || $this->automation !== null)) {
            throw new InvalidArgumentException('Relay plans cannot contain an immediate response.');
        }

        if ($this->action === self::RELAY && $this->reply === null && $this->relays === []) {
            throw new InvalidArgumentException('Relay plans require a reply or relay destination.');
        }

        if ($this->action === self::AUTOMATION
            && ($this->automation === null || $this->immediateResponse !== null || $this->reply !== null)) {
            throw new InvalidArgumentException('Automation plans require automation and can only contain relay destinations.');
        }

        if ($this->immediateResponse !== null && ($this->immediateResponse->statusCode < 100 || $this->immediateResponse->statusCode > 599)) {
            throw new InvalidArgumentException('Response status code is outside the HTTP range.');
        }

        foreach (array_filter([$this->reply, ...$this->relays]) as $delivery) {
            if (! $delivery instanceof Delivery) {
                throw new InvalidArgumentException('Relay destinations must be Delivery objects.');
            }

            $delivery->assertValid();
        }
    }
}
