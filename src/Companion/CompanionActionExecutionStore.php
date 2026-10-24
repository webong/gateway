<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Support\Facades\DB;
use RuntimeException;
use Throwable;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

final readonly class CompanionActionExecutionStore
{
    public function __construct(private CompanionActionRegistry $actions) {}

    public function execute(CompanionActionInvocation $invocation): CompanionActionResult
    {
        $this->assertCompatible($invocation);
        $fingerprint = $invocation->fingerprint();
        $idempotencyHash = hash('sha256', $invocation->idempotencyKey);
        $now = now('UTC');

        $created = DB::table('companion_action_executions')->insertOrIgnore([
            'execution_id' => $invocation->executionId,
            'action' => $invocation->action,
            'companion_version' => $invocation->companionVersion,
            'companion_digest' => $invocation->companionDigest,
            'idempotency_hash' => $idempotencyHash,
            'request_hash' => $fingerprint,
            'status' => 'running',
            'result' => null,
            'started_at' => $now,
            'completed_at' => null,
            'created_at' => $now,
            'updated_at' => $now,
        ]) === 1;

        if (! $created) {
            $existing = DB::table('companion_action_executions')
                ->where('execution_id', $invocation->executionId)
                ->orWhere(static fn ($query) => $query
                    ->where('action', $invocation->action)
                    ->where('idempotency_hash', $idempotencyHash))
                ->first();
            if ($existing === null) {
                throw new RuntimeException('Gateway companion action execution could not be claimed.');
            }
            if (! hash_equals((string) $existing->request_hash, $fingerprint)) {
                throw new CompanionActionIdempotencyConflict('Gateway companion action idempotency key was reused with different input.');
            }
            if (is_string($existing->result) && $existing->result !== '') {
                $decoded = json_decode($existing->result, true, 128, JSON_THROW_ON_ERROR);

                return CompanionActionResult::fromArray($decoded);
            }

            return CompanionActionResult::inProgress((string) $existing->execution_id);
        }

        try {
            $result = $this->actions->invoke($invocation);
        } catch (Throwable $exception) {
            report($exception);
            $result = CompanionActionResult::failed(
                $invocation->executionId,
                'companion_action_failed',
                'The companion action failed.',
            );
        }

        $updated = DB::table('companion_action_executions')
            ->where('execution_id', $invocation->executionId)
            ->where('status', 'running')
            ->update([
                'status' => $result->status,
                'result' => json_encode($result->toArray(), JSON_THROW_ON_ERROR),
                'completed_at' => now('UTC'),
                'updated_at' => now('UTC'),
            ]);
        if ($updated !== 1) {
            throw new RuntimeException('Gateway companion action result could not be recorded.');
        }

        return $result;
    }

    private function assertCompatible(CompanionActionInvocation $invocation): void
    {
        $capability = $this->actions->capability($invocation->action);
        if ($capability === null) {
            throw new CompanionActionNotFound("Gateway companion action [{$invocation->action}] is not installed.");
        }
        if ($invocation->companionVersion !== null && $invocation->companionVersion !== $capability->version) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] version is incompatible.");
        }
        if ($invocation->companionDigest !== null && $invocation->companionDigest !== $capability->digest) {
            throw new CompanionActionIncompatible("Gateway companion action [{$invocation->action}] digest is incompatible.");
        }
    }
}
