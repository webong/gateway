<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Support\Facades\Crypt;
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
        $claimToken = bin2hex(random_bytes(16));

        $created = DB::table('companion_action_executions')->insertOrIgnore([
            'execution_id' => $invocation->executionId,
            'action' => $invocation->action,
            'companion_version' => $invocation->companionVersion,
            'companion_digest' => $invocation->companionDigest,
            'idempotency_hash' => $idempotencyHash,
            'request_hash' => $fingerprint,
            'status' => 'running',
            'attempts' => 1,
            'claim_token' => $claimToken,
            'lease_expires_at' => $invocation->deadline,
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
            if (! hash_equals((string) $existing->idempotency_hash, $idempotencyHash)
                || ! hash_equals((string) $existing->action, $invocation->action)
                || ! hash_equals((string) $existing->request_hash, $fingerprint)) {
                throw new CompanionActionIdempotencyConflict('Gateway companion action idempotency key was reused with different input.');
            }
            if (is_string($existing->result) && $existing->result !== '') {
                $decoded = json_decode(Crypt::decryptString($existing->result), true, 128, JSON_THROW_ON_ERROR);

                return CompanionActionResult::fromArray($decoded);
            }
            if ($existing->lease_expires_at !== null && now('UTC')->lt($existing->lease_expires_at)) {
                return CompanionActionResult::inProgress((string) $existing->execution_id);
            }

            $claimToken = bin2hex(random_bytes(16));
            $claimed = DB::table('companion_action_executions')
                ->where('execution_id', $existing->execution_id)
                ->where('status', 'running')
                ->where(static fn ($query) => $query
                    ->whereNull('lease_expires_at')
                    ->orWhere('lease_expires_at', '<=', now('UTC')))
                ->update([
                    'claim_token' => $claimToken,
                    'lease_expires_at' => $invocation->deadline,
                    'attempts' => DB::raw('attempts + 1'),
                    'updated_at' => now('UTC'),
                ]);
            if ($claimed !== 1) {
                return CompanionActionResult::inProgress((string) $existing->execution_id);
            }
            $invocation = $invocation->withExecutionId((string) $existing->execution_id);
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
            ->where('claim_token', $claimToken)
            ->update([
                'status' => $result->status,
                'result' => Crypt::encryptString(json_encode($result->toArray(), JSON_THROW_ON_ERROR)),
                'claim_token' => null,
                'lease_expires_at' => null,
                'completed_at' => now('UTC'),
                'updated_at' => now('UTC'),
            ]);
        if ($updated !== 1) {
            throw new RuntimeException('Gateway companion action result could not be recorded.');
        }

        return $result;
    }

    public function prune(int $retentionDays): int
    {
        if ($retentionDays < 1 || $retentionDays > 3650) {
            throw new \InvalidArgumentException('Gateway companion action retention must be between 1 and 3650 days.');
        }

        return DB::table('companion_action_executions')
            ->where('updated_at', '<', now('UTC')->subDays($retentionDays))
            ->delete();
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
