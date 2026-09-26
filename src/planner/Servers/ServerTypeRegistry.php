<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers;

use InvalidArgumentException;
use Webong\Gateway\Servers\Contracts\ServerType;

final class ServerTypeRegistry
{
    /** @var array<string, ServerType> */
    private array $types = [];

    public function register(ServerType $type): void
    {
        $name = strtolower(trim($type->type()));
        if ($name === '') {
            throw new InvalidArgumentException('A managed server type must have a name.');
        }
        if (isset($this->types[$name]) && $this->types[$name] !== $type) {
            throw new InvalidArgumentException("Managed server type [{$name}] is already registered.");
        }

        $this->types[$name] = $type;
    }

    public function find(string $type): ?ServerType
    {
        return $this->types[strtolower($type)] ?? null;
    }

    /** @return list<string> */
    public function names(): array
    {
        $names = array_keys($this->types);
        sort($names);

        return $names;
    }
}
