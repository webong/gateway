<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

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
            // Empty PHP arrays encode as JSON lists. The bridge contract is a
            // string-to-list map, so preserve an empty JSON object for Go.
            'headers' => $this->headers === [] ? (object) [] : $this->headers,
            'body' => base64_encode($this->body),
        ];
    }
}
