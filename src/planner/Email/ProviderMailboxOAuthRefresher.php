<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Http;
use RuntimeException;
use Throwable;

/** Refreshes delegated IMAP/SMTP credentials at fixed provider token endpoints. */
final class ProviderMailboxOAuthRefresher implements MailboxOAuthRefresher
{
    public function refresh(string $provider, string $side, string $refreshToken): array
    {
        if (! in_array($side, ['imap', 'smtp'], true) || $refreshToken === '' || strlen($refreshToken) > 8192) {
            throw new RuntimeException('Mailbox OAuth refresh is invalid.');
        }
        $settings = config('gateway.email.oauth.'.$provider);
        $clientId = $settings['client_id'] ?? null;
        $clientSecret = $settings['client_secret'] ?? null;
        if (! is_string($clientId) || $clientId === '' || ! is_string($clientSecret) || $clientSecret === '') {
            throw new RuntimeException('Mailbox OAuth provider is not configured.');
        }
        $body = ['client_id' => $clientId, 'client_secret' => $clientSecret,
            'refresh_token' => $refreshToken, 'grant_type' => 'refresh_token'];
        if ($provider === 'google') {
            $url = 'https://oauth2.googleapis.com/token';
        } elseif ($provider === 'microsoft') {
            $tenant = $settings['tenant'] ?? null;
            if (! is_string($tenant) || preg_match('/\A(?:common|organizations|consumers|[a-fA-F0-9-]{36})\z/D', $tenant) !== 1) {
                throw new RuntimeException('Mailbox OAuth tenant is invalid.');
            }
            $url = 'https://login.microsoftonline.com/'.$tenant.'/oauth2/v2.0/token';
            $body['scope'] = $side === 'imap'
                ? 'https://outlook.office.com/IMAP.AccessAsUser.All'
                : 'https://outlook.office.com/SMTP.Send';
        } else {
            throw new RuntimeException('Mailbox OAuth provider is invalid.');
        }
        try { $response = Http::asForm()->acceptJson()->timeout(5)->post($url, $body); }
        catch (Throwable) { throw new RuntimeException('Mailbox OAuth provider is unavailable.'); }
        if (! $response->ok()) { throw new RuntimeException('Mailbox OAuth provider rejected the refresh token.'); }
        $tokens = $response->json();
        if (! is_array($tokens) || ! is_string($tokens['access_token'] ?? null)
            || $tokens['access_token'] === '' || strlen($tokens['access_token']) > 4096) {
            throw new RuntimeException('Mailbox OAuth provider returned an invalid token.');
        }
        $result = ['access_token' => $tokens['access_token']];
        if (is_string($tokens['refresh_token'] ?? null) && $tokens['refresh_token'] !== '') {
            $result['refresh_token'] = $tokens['refresh_token'];
        }

        return $result;
    }
}
