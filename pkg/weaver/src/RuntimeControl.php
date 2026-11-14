<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

/** Process-local control plane. Never exposed by the mounted workflow RPC route. */
final readonly class RuntimeControl
{
    public function __construct(private RuntimeFactory $runtime) {}

    public function invoke(string $method, array $params): array
    {
        if ($method === 'work.reserve') {
            if ($params !== []) { throw new RpcFailure(-32602, 'Invalid params'); }
            return ['events' => $this->runtime->queue->reserveEvents(1, 30, 'work.ready')];
        }
        if ($method === 'work.handle') {
            if (array_keys($params) !== ['claim_id'] || !$this->identifier($params['claim_id'])) {
                throw new RpcFailure(-32602, 'Invalid params');
            }
            // Duplicate, executing, completed, cancelled and expired claims all
            // pass through the queue's atomic execution gate; never inline fallback.
            return ['handled' => $this->runtime->worker()->handle($params['claim_id'])];
        }
        if (in_array($method, ['work.ack', 'work.retry'], true)) {
            if (array_diff(array_keys($params), ['id', 'token']) !== []
                || !$this->identifier($params['id'] ?? null) || !$this->identifier($params['token'] ?? null)) {
                throw new RpcFailure(-32602, 'Invalid params');
            }
            if ($method === 'work.ack') { $this->runtime->queue->acknowledgeEvent($params['id'], $params['token']); }
            else { $this->runtime->queue->retryEvent($params['id'], $params['token']); }
            return [];
        }
        throw new RpcFailure(-32601, 'Method not found');
    }

    private function identifier(mixed $value): bool
    {
        return is_string($value) && preg_match('/\A[a-f0-9]{32}\z/D', $value) === 1;
    }
}
