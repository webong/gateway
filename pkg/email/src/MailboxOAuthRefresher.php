<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** Return a fresh access token and optional rotated refresh token. */
interface MailboxOAuthRefresher
{
    /** @return array{access_token: string, refresh_token?: string} */
    public function refresh(string $provider, string $side, string $refreshToken): array;
}
