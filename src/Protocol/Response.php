<?php

declare(strict_types=1);

namespace Webong\WebRelay\Protocol;

final readonly class Response
{
    /** @param array<string, list<string>> $headers */
    public function __construct(
        public int $statusCode = 200,
        public array $headers = [],
        public string $body = '',
    ) {
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return [
            'status_code' => $this->statusCode,
            'headers' => $this->headers,
            'body' => base64_encode($this->body),
        ];
    }
}
