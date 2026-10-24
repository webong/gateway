<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use DateTimeImmutable;
use InvalidArgumentException;
use Webong\Gateway\Contracts\CompanionAction;
use Webong\Gateway\Protocols\CompanionActionInvocation;

/** Minimal ext.plugin/v1 JSON-line guest for self-contained PHP companions. */
final readonly class CompanionProcessGuest
{
    private const string API_VERSION = 'ext.plugin/v1';

    private const int MAX_FRAME_BYTES = 25165824;

    /**
     * @param array<string, CompanionAction> $actions
     */
    public function __construct(
        private string $extension,
        private string $revision,
        private ?string $version,
        private array $actions,
    ) {
        if (preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $extension) !== 1
            || preg_match('/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/D', $revision) !== 1
            || ($version !== null && preg_match('/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/D', $version) !== 1)
            || $actions === []) {
            throw new InvalidArgumentException('Invalid Gateway companion process identity.');
        }
        foreach ($actions as $name => $action) {
            if (! is_string($name) || ! $action instanceof CompanionAction) {
                throw new InvalidArgumentException('Invalid Gateway companion process action.');
            }
        }
    }

    /**
     * Serve ext JSON-line frames until the host closes stdin.
     *
     * @param resource|null $input
     * @param resource|null $output
     */
    public function serve(mixed $input = null, mixed $output = null): int
    {
        $input ??= STDIN;
        $output ??= STDOUT;
        $hello = false;
        while (($line = fgets($input, self::MAX_FRAME_BYTES + 2)) !== false) {
            if (strlen($line) > self::MAX_FRAME_BYTES || ! str_ends_with($line, "\n")) {
                return 1;
            }
            try {
                $request = json_decode(rtrim($line, "\r\n"), true, 128, JSON_THROW_ON_ERROR);
                if (! is_array($request)) {
                    return 1;
                }
                if (! $hello) {
                    $this->assertHello($request);
                    $response = $this->response('hello', $this->descriptor());
                    $hello = true;
                } else {
                    $response = $this->dispatch($request);
                }
                fwrite($output, json_encode($response, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES)."\n");
                fflush($output);
            } catch (\Throwable) {
                return 1;
            }
        }

        return $hello ? 0 : 1;
    }

    /** @return array<string, mixed> */
    public function descriptor(): array
    {
        return [
            'apiVersion' => self::API_VERSION,
            'identity' => array_filter([
                'id' => $this->extension,
                'revision' => $this->revision,
                'version' => $this->version,
            ], static fn (mixed $value): bool => $value !== null),
            'contracts' => [[
                'name' => 'gateway.companion',
                'version' => CompanionActionInvocation::VERSION,
                'operations' => array_map(
                    static fn (string $name): array => ['name' => $name],
                    array_keys($this->actions),
                ),
            ]],
        ];
    }

    /** @param array<string, mixed> $request */
    private function assertHello(array $request): void
    {
        $deadline = $request['deadline'] ?? null;
        if (($request['apiVersion'] ?? null) !== self::API_VERSION
            || ($request['id'] ?? null) !== 'hello'
            || ($request['operation'] ?? null) !== 'plugin.hello'
            || ! is_string($deadline)
            || new DateTimeImmutable($deadline) <= new DateTimeImmutable('now')) {
            throw new InvalidArgumentException('Invalid ext plugin hello request.');
        }
    }

    /**
     * @param array<string, mixed> $request
     * @return array<string, mixed>
     */
    private function dispatch(array $request): array
    {
        $id = $request['id'] ?? null;
        $operation = $request['operation'] ?? null;
        $contract = $request['contract'] ?? null;
        $identity = $request['plugin'] ?? null;
        $deadline = $request['deadline'] ?? null;
        if (($request['apiVersion'] ?? null) !== self::API_VERSION
            || ! is_string($id) || preg_match('/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/D', $id) !== 1
            || ! is_string($operation) || ! isset($this->actions[$operation])
            || ! is_array($contract) || $contract !== ['name' => 'gateway.companion', 'version' => CompanionActionInvocation::VERSION]
            || ! is_array($identity) || $identity != $this->descriptor()['identity']
            || ! is_string($deadline) || new DateTimeImmutable($deadline) <= new DateTimeImmutable('now')
            || ! is_array($request['payload'] ?? null)) {
            throw new InvalidArgumentException('Invalid ext companion invocation.');
        }
        $invocation = CompanionActionInvocation::fromArray($request['payload']);
        if (! hash_equals($operation, $invocation->action)) {
            throw new InvalidArgumentException('Companion operation does not match invocation action.');
        }

        return $this->response($id, $this->actions[$operation]->handle($invocation)->toArray());
    }

    /** @return array{apiVersion: string, id: string, payload: mixed} */
    private function response(string $id, mixed $payload): array
    {
        return ['apiVersion' => self::API_VERSION, 'id' => $id, 'payload' => $payload];
    }
}
