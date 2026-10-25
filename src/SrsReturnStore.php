<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;

/** Durable return-path ownership shared by all Planner workers. */
final readonly class SrsReturnStore
{
    public function __construct(
        private EmailSrsPolicyStore $policy,
        private EmailRegistry $domains,
    ) {}

    public function rewriteEnabled(): bool
    {
        return $this->policy->rewriteEnabled();
    }

    public function isCandidate(string $address): bool
    {
        $codec = $this->policy->codec();
        $separator = strrpos($address, '@');

        return $codec !== null && $separator !== false
            && strcasecmp(substr($address, $separator + 1), $codec->domain) === 0
            && preg_match('/^SRS[01]-/i', substr($address, 0, $separator)) === 1;
    }

    public function issue(string $sender): string
    {
        if (! $this->policy->rewriteEnabled() || ($codec = $this->policy->codec()) === null) {
            throw new EmailRouteRejected('SRS rewriting is disabled.');
        }
        if (filter_var($sender, FILTER_VALIDATE_EMAIL) === false) {
            throw new EmailRouteRejected('The SMTP reverse-path is invalid.');
        }

        $this->domains->requireActiveDomain($codec->domain);
        $token = $codec->issue();
        $now = now();
        DB::table('srs_returns')->insert([
            'token_hash' => $token->hash,
            'original_sender' => Crypt::encryptString($sender),
            'expires_at' => Carbon::createFromTimestamp($token->expiresAt),
            'created_at' => $now,
            'updated_at' => $now,
        ]);
        // Indexed, opportunistic cleanup avoids an unbounded token table on
        // active installations without requiring a second scheduler.
        if (random_int(1, 1000) === 1) {
            $this->prune();
        }

        return $token->address;
    }

    public function resolve(string $address): ?string
    {
        $token = $this->policy->codec()?->verify($address);
        if ($token === null) {
            return null;
        }
        $row = DB::table('srs_returns')->where('token_hash', $token->hash)->first();
        if ($row === null || Carbon::parse($row->expires_at)->isPast()) {
            return null;
        }

        $sender = Crypt::decryptString($row->original_sender);

        return filter_var($sender, FILTER_VALIDATE_EMAIL) !== false ? $sender : null;
    }

    public function prune(): int
    {
        return DB::table('srs_returns')->where('expires_at', '<', now())->delete();
    }
}
