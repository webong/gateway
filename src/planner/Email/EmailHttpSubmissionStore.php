<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Database\QueryException;
use Illuminate\Support\Facades\DB;
use RuntimeException;
use stdClass;

/** Pins one HTTP submission key to a durable receipt and Spinner node. */
final readonly class EmailHttpSubmissionStore
{
    public function __construct(private EmailSubmissionQuota $quota, private EmailSuppressionStore $suppressions) {}

    /** @param non-empty-array<string, string> $nodes */
    public function reserve(string $identity, string $mailFrom, array $recipients, string $key, string $requestHash, array $nodes, int $limit, int $messageBytes, int $byteLimit): stdClass
    {
        if ($nodes === [] || strlen($requestHash) !== 64) {
            throw new RuntimeException('HTTP Email submission requires a valid node and message fingerprint.');
        }
        $identityHash = hash('sha256', $identity);
        $keyHash = hash('sha256', $key);
        $nodeNames = array_keys($nodes);

        for ($attempt = 0; $attempt < 2; $attempt++) {
            try {
                return DB::transaction(function () use ($identity, $mailFrom, $recipients, $identityHash, $keyHash, $requestHash, $nodeNames, $limit, $messageBytes, $byteLimit): stdClass {
                    $now = now('UTC');
                    $query = DB::table('email_http_submissions')
                        ->where('identity_hash', $identityHash)->where('key_hash', $keyHash);
                    $existing = (clone $query)->lockForUpdate()->first();
                    if ($existing !== null && (string) $existing->expires_at > $now->toDateTimeString()) {
                        if ($existing->request_hash !== $requestHash) {
                            throw new EmailSubmissionKeyConflict('The idempotency key belongs to a different email submission.');
                        }

                        return $existing;
                    }
                    if ($existing !== null) {
                        $query->delete();
                    }

                    if ($this->suppressions->containsAny($recipients)) {
                        throw new EmailRecipientSuppressed('One or more Email recipients are suppressed.');
                    }

                    $node = $nodeNames[hexdec(substr($identityHash, 0, 7)) % count($nodeNames)];
                    $row = [
                        'identity_hash' => $identityHash,
                        'key_hash' => $keyHash,
                        'request_hash' => $requestHash,
                        'receipt_id' => bin2hex(random_bytes(16)),
                        'node' => $node,
                        'accepted_at' => null,
                        'expires_at' => $now->copy()->addDay(),
                        'created_at' => $now,
                        'updated_at' => $now,
                    ];
                    DB::table('email_http_submissions')->insert($row);
                    if (! $this->quota->consumeSubmission($identity, $mailFrom, $limit, $messageBytes, $byteLimit)) {
                        throw new EmailSubmissionLimitExceeded('Daily submission quota exceeded.');
                    }

                    return (object) $row;
                });
            } catch (QueryException $exception) {
                if ($attempt === 1) {
                    throw $exception;
                }
            }
        }

        throw new RuntimeException('HTTP Email submission reservation failed.');
    }

    public function markAccepted(string $identity, string $key, string $receiptId): void
    {
        DB::table('email_http_submissions')
            ->where('identity_hash', hash('sha256', $identity))
            ->where('key_hash', hash('sha256', $key))
            ->where('receipt_id', $receiptId)
            ->update(['accepted_at' => now('UTC'), 'updated_at' => now('UTC')]);
    }

    public function prune(): int
    {
        return DB::table('email_http_submissions')->where('expires_at', '<', now('UTC'))->delete();
    }
}
