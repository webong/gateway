<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;

/** Atomic per-account and per-sender-domain UTC-day submission counters. */
final class EmailSubmissionQuota
{
    public function __construct(private readonly EmailSubmissionPolicyStore $policies) {}

    public function consume(string $identity, int $limit, int $messageBytes = 0, int $byteLimit = 0): bool
    {
        if ($identity === '' || $limit < 0 || $limit > 1_000_000 || $byteLimit < 0 || $byteLimit > 1_000_000_000_000
            || ($limit === 0 && $byteLimit === 0) || $messageBytes < 0 || $messageBytes > 1_000_000_000_000) {
            throw new InvalidArgumentException('Email submission quota requires a canonical identity and valid limits.');
        }

        if ($byteLimit > 0 && $messageBytes > $byteLimit) {
            return false;
        }

        return $this->consumeBucket('email.submission.account', hash('sha256', $identity), $limit, $messageBytes, $byteLimit);
    }

    /** Apply configured sender-domain caps across accounts, atomically with account caps. */
    public function consumeSubmission(string $identity, string $mailFrom, int $accountLimit, int $messageBytes, int $accountByteLimit): bool
    {
        $policy = $this->policies->limits();
        $domainLimit = $policy['domain_daily_limit'];
        $domainByteLimit = $policy['domain_daily_byte_limit'];
        if ($domainLimit < 0 || $domainLimit > 1_000_000 || $domainByteLimit < 0 || $domainByteLimit > 1_000_000_000_000) {
            throw new InvalidArgumentException('Email submission domain quota policy is invalid.');
        }
        if ($accountLimit === 0 && $accountByteLimit === 0 && $domainLimit === 0 && $domainByteLimit === 0) {
            return true;
        }
        if ($domainLimit === 0 && $domainByteLimit === 0) {
            return $this->consume($identity, $accountLimit, $messageBytes, $accountByteLimit);
        }
        $separator = strrpos($mailFrom, '@');
        $domain = $separator === false ? '' : strtolower(rtrim(substr($mailFrom, $separator + 1), '.'));
        if ($identity === '' || $domain === '' || strlen($domain) > 253 || $messageBytes < 0
            || $messageBytes > 1_000_000_000_000 || preg_match('/\A[a-z0-9.-]+\z/D', $domain) !== 1) {
            throw new InvalidArgumentException('Email submission requires a valid identity and sender domain.');
        }
        if ($domainByteLimit > 0 && $messageBytes > $domainByteLimit) {
            return false;
        }

        try {
            return DB::transaction(function () use ($identity, $accountLimit, $messageBytes, $accountByteLimit, $domain, $domainLimit, $domainByteLimit): bool {
                if (($accountLimit > 0 || $accountByteLimit > 0)
                    && ! $this->consume($identity, $accountLimit, $messageBytes, $accountByteLimit)) {
                    throw new EmailSubmissionLimitExceeded('Account submission quota exceeded.');
                }
                if (! $this->consumeBucket('email.submission.domain', hash('sha256', $domain), $domainLimit, $messageBytes, $domainByteLimit)) {
                    throw new EmailSubmissionLimitExceeded('Sender domain submission quota exceeded.');
                }

                return true;
            });
        } catch (EmailSubmissionLimitExceeded) {
            return false;
        }
    }

    public function prune(int $retentionDays): int
    {
        if ($retentionDays < 1) {
            throw new InvalidArgumentException('Email submission usage retention must be positive.');
        }

        return DB::table('usage_counters')
            ->whereIn('metric', ['email.submission.account', 'email.submission.domain'])
            ->where('day', '<', now('UTC')->subDays($retentionDays)->toDateString())
            ->delete();
    }

    private function consumeBucket(string $metric, string $hash, int $limit, int $messageBytes, int $byteLimit): bool
    {
        $now = now('UTC');
        $day = $now->toDateString();
        DB::table('usage_counters')->insertOrIgnore([
            'metric' => $metric,
            'subject_hash' => $hash,
            'day' => $day,
            'attempts' => 0,
            'bytes' => 0,
            'created_at' => $now,
            'updated_at' => $now,
        ]);

        $query = DB::table('usage_counters')->where('metric', $metric)->where('subject_hash', $hash)->where('day', $day);
        if ($limit > 0) {
            $query->where('attempts', '<', $limit);
        }
        if ($byteLimit > 0) {
            $query->where('bytes', '<=', $byteLimit - $messageBytes);
        }

        return $query->update([
            'attempts' => DB::raw('attempts + 1'),
            'bytes' => DB::raw('bytes + '.(string) $messageBytes),
            'updated_at' => $now,
        ]) === 1;
    }
}
