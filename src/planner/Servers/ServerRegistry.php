<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers;

use Illuminate\Database\ConnectionInterface;
use Illuminate\Support\Str;
use LogicException;
use Webong\Gateway\Servers\Contracts\ApplicationServerType;
use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Application;
use Webong\Gateway\Servers\Models\Server;

final readonly class ServerRegistry
{
    public function __construct(private ConnectionInterface $database) {}

    /** @param array<string, mixed> $attributes */
    public function createServer(ServerType $type, array $attributes): Server
    {
        return $this->database->transaction(function () use ($type, $attributes): Server {
            $id = (string) Str::uuid();
            $configuration = $type->configurationForStorage(
                $attributes['configuration'] ?? [],
                [],
                $id,
            );

            $server = new Server;
            $server->id = $id;
            $server->fill([
                'type' => $type->type(),
                'name' => $attributes['name'],
                'hostname' => $attributes['hostname'],
                'path' => self::normalizePath((string) ($attributes['path'] ?? $type->defaultPath())),
                'driver' => $attributes['driver'] ?? 'local',
                'desired_state' => 'stopped',
                'replicas' => $attributes['replicas'] ?? 1,
                'revision' => 1,
                'configuration' => $configuration,
            ]);
            $server->save();

            return $server->refresh();
        });
    }

    /** @param array<string, mixed> $attributes */
    public function updateServer(Server $server, ServerType $type, array $attributes): Server
    {
        return $this->database->transaction(function () use ($server, $type, $attributes): Server {
            $locked = Server::query()->lockForUpdate()->findOrFail($server->getKey());
            if (array_key_exists('path', $attributes)) {
                $attributes['path'] = self::normalizePath((string) $attributes['path']);
            }
            if (array_key_exists('configuration', $attributes)) {
                $attributes['configuration'] = $type->configurationForStorage(
                    $attributes['configuration'],
                    $locked->configuration ?? [],
                    (string) $locked->getKey(),
                );
            }
            $locked->fill($attributes);
            $locked->revision++;
            $locked->save();

            return $locked->refresh();
        });
    }

    public function setDesiredState(Server $server, string $state, bool $replace = false): Server
    {
        return $this->database->transaction(function () use ($server, $state, $replace): Server {
            $locked = Server::query()->lockForUpdate()->findOrFail($server->getKey());
            $locked->desired_state = $state;
            if ($replace) {
                $locked->revision++;
            }
            $locked->save();

            return $locked->refresh();
        });
    }

    public function deleteServer(Server $server): void
    {
        if ($server->desired_state !== 'stopped' || $server->instances()->whereIn('state', ['starting', 'running'])->exists()) {
            throw new LogicException('Stop the server before deleting it.');
        }

        $server->delete();
    }

    /** @param array<string, mixed> $validated */
    public function createApplication(Server $server, ApplicationServerType $type, array $validated): Application
    {
        return $this->database->transaction(function () use ($server, $type, $validated): Application {
            return $server->applications()->create([
                ...$type->applicationForStorage($validated, null),
                'revision' => 1,
            ])->refresh();
        });
    }

    /** @param array<string, mixed> $validated */
    public function updateApplication(Application $application, ApplicationServerType $type, array $validated): Application
    {
        return $this->database->transaction(function () use ($application, $type, $validated): Application {
            $locked = Application::query()->lockForUpdate()->findOrFail($application->getKey());
            $locked->fill($type->applicationForStorage($validated, $locked));
            $locked->revision++;
            $locked->save();

            return $locked->refresh();
        });
    }

    public static function normalizePath(string $path): string
    {
        $path = trim($path);
        if ($path === '' || $path === '/') {
            return '';
        }

        return '/'.trim($path, '/');
    }
}
