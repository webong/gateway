<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

/** HTTP authentication supplies account; untrusted RPC params cannot select it. */
final readonly class AuthorizedRpcApplication
{
    /** @param list<RunGrant> $grants */
    public function __construct(private FlowRpcApplication $application, private array $grants) {}

    /** @param array<string, mixed> $params @return array<string, mixed> */
    public function invoke(string $account, string $method, array $params): array
    {
        if (! in_array($method, RunGrant::ACTIONS, true)) {
            throw new RpcFailure(-32601, 'Method not found');
        }
        if ($account === '' || array_key_exists('account', $params) || array_key_exists('principal', $params)) {
            throw new RpcFailure(-32003, 'Forbidden');
        }
        $allowed = false;
        foreach ($this->grants as $grant) {
            if ($grant->permits($account, $method, $params)) { $allowed = true; break; }
        }
        if (! $allowed) { throw new RpcFailure(-32003, 'Forbidden'); }
        // The legacy RPC adapter scopes by subject/flow/run. Include authenticated
        // account in that storage identity while retaining the public logical ID.
        $subject = $params['subject'];
        $params['subject'] = ['type' => 'gateway.workflow.account', 'id' => hash('sha256', json_encode(
            [$account, $subject['type'], $subject['id']], JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES,
        ))];
        if ($method === 'flow.start') {
            if (isset($params['driver']) && $params['driver'] !== 'queue') {
                throw new RpcFailure(-32602, 'Unknown or unavailable execution driver');
            }
            $params['driver'] = 'queue';
        }
        return $this->application->invoke($method, $params);
    }
}
