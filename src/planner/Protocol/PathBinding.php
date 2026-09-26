<?php

declare(strict_types=1);

namespace Webong\Gateway\Protocol;

use InvalidArgumentException;

final readonly class PathBinding
{
    /** @param array<string, mixed> $destinationMetadata */
    public function __construct(
        public string $endpointKey,
        public string $scope = 'path',
        public string $key = '',
        public ?string $channel = null,
        public array $destinationMetadata = [],
    ) {
        if ($this->endpointKey === '') {
            throw new InvalidArgumentException('A path binding requires an endpoint key.');
        }
    }

    public function routeKey(IngressRequest $request): string
    {
        return $this->key !== '' ? $this->key : $request->path;
    }
}
