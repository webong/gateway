<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use InvalidArgumentException;
use RuntimeException;
use Throwable;

/** One-use, account-bound authorization-code transactions; provider secrets never leave Gateway. */
final readonly class MailboxOAuthAuthorization
{
    public function __construct(private MailboxConnectionStore $connections,
        private MailboxConnectionTester $tester, private EmailUpstreamCatalog $upstreams) {}

    public function start(string $account, array $input): array
    {
        if (array_diff(array_keys($input), ['provider', 'mode', 'address', 'sending', 'connection_id']) !== []
            || ! in_array($input['provider'] ?? null, ['google', 'microsoft'], true)
            || ! in_array($input['mode'] ?? null, ['imap', 'mailbox'], true)
            || ! is_bool($input['sending'] ?? null)
            || $input['sending'] !== ($input['mode'] === 'mailbox')
            || ! is_string($input['address'] ?? null)
            || strlen($input['address']) > 320 || ! filter_var($input['address'], FILTER_VALIDATE_EMAIL)
            || (isset($input['connection_id']) && (! is_string($input['connection_id'])
                || $this->connections->find($account, $input['connection_id'])?->mode !== $input['mode']))) {
            throw new InvalidArgumentException('Invalid mailbox authorization request.');
        }
        $provider = $input['provider'];
        $settings = $this->settings($provider);
        $imap = $settings['imap_upstream_id'] ?? null;
        $smtp = $settings['smtp_upstream_id'] ?? null;
        if (! is_string($imap) || $this->upstreams->active('imap', $imap) === null
            || ($input['sending'] && (! is_string($smtp) || $this->upstreams->active('smtp', $smtp) === null))) {
            throw new RuntimeException('Mailbox provider is unavailable.');
        }
        $state = self::base64url(random_bytes(32));
        $verifier = self::base64url(random_bytes(32));
        $scope = $provider === 'google' ? 'https://mail.google.com/'
            : 'offline_access https://outlook.office.com/IMAP.AccessAsUser.All'
                .($input['sending'] ? ' https://outlook.office.com/SMTP.Send' : '');
        $document = ['provider' => $provider, 'mode' => $input['mode'],
            'address' => strtolower($input['address']), 'sending' => $input['sending'],
            'connection_id' => $input['connection_id'] ?? null, 'verifier' => $verifier,
            'callback_url' => $settings['callback_url'], 'imap_upstream_id' => $imap,
            'smtp_upstream_id' => $input['sending'] ? $smtp : null, 'scope' => $scope];
        DB::table('mailbox_oauth_transactions')->insert([
            'state_hash' => hash('sha256', $state), 'account_id' => $account,
            'ciphertext' => Crypt::encryptString(json_encode($document, JSON_THROW_ON_ERROR)),
            'expires_at' => now('UTC')->addMinutes(10), 'created_at' => now('UTC'), 'updated_at' => now('UTC'),
        ]);
        $base = $provider === 'google' ? 'https://accounts.google.com/o/oauth2/v2/auth'
            : 'https://login.microsoftonline.com/'.$settings['tenant'].'/oauth2/v2.0/authorize';
        $query = ['client_id' => $settings['client_id'], 'redirect_uri' => $settings['callback_url'],
            'response_type' => 'code', 'scope' => $scope, 'state' => $state,
            'code_challenge' => self::base64url(hash('sha256', $verifier, true)), 'code_challenge_method' => 'S256'];
        if ($provider === 'google') {
            $query['access_type'] = 'offline';
            $query['prompt'] = 'consent';
            $query['login_hint'] = $document['address'];
        } else {
            $query['login_hint'] = $document['address'];
        }
        return ['authorization_url' => $base.'?'.http_build_query($query, '', '&', PHP_QUERY_RFC3986),
            'state' => $state, 'expires_at' => now('UTC')->addMinutes(10)->toIso8601String()];
    }

    public function complete(string $account, array $input): array
    {
        if (array_diff(array_keys($input), ['provider', 'state', 'code', 'error', 'callback_url']) !== []
            || ! in_array($input['provider'] ?? null, ['google', 'microsoft'], true)
            || ! is_string($input['state'] ?? null) || preg_match('/^[A-Za-z0-9_-]{43}$/D', $input['state']) !== 1
            || ! is_string($input['callback_url'] ?? null)
            || (isset($input['code']) === isset($input['error']))
            || (isset($input['code']) && (! is_string($input['code']) || $input['code'] === '' || strlen($input['code']) > 4096))
            || (isset($input['error']) && (! is_string($input['error']) || strlen($input['error']) > 256))) {
            throw new InvalidArgumentException('Invalid mailbox authorization callback.');
        }
        $hash = hash('sha256', $input['state']);
        $row = DB::table('mailbox_oauth_transactions')->where('state_hash', $hash)
            ->where('account_id', $account)->first();
        if ($row === null || now('UTC')->greaterThanOrEqualTo($row->expires_at)) {
            throw new RuntimeException('Mailbox authorization expired or invalid.');
        }
        $document = json_decode(Crypt::decryptString($row->ciphertext), true, 512, JSON_THROW_ON_ERROR);
        if ($document['provider'] !== $input['provider'] || $document['callback_url'] !== $input['callback_url']
            || $document['callback_url'] !== $this->settings($input['provider'])['callback_url']) {
            throw new RuntimeException('Mailbox authorization expired or invalid.');
        }
        $codeHash = hash('sha256', $input['code'] ?? 'error:'.$input['error']);
        if ($row->status === 'complete' && hash_equals((string) $row->code_hash, $codeHash)
            && $row->result_ciphertext !== null) {
            return json_decode(Crypt::decryptString($row->result_ciphertext), true, 512, JSON_THROW_ON_ERROR);
        }
        if ($row->status !== 'pending') { throw new RuntimeException('Mailbox authorization already used.'); }
        $claimed = DB::table('mailbox_oauth_transactions')->where('state_hash', $hash)
            ->where('account_id', $account)->where('status', 'pending')->where('expires_at', '>', now('UTC'))
            ->update(['status' => 'processing', 'code_hash' => $codeHash, 'updated_at' => now('UTC')]);
        if ($claimed !== 1) { throw new RuntimeException('Mailbox authorization already used.'); }
        if (isset($input['error'])) { throw new RuntimeException('Mailbox authorization was declined.'); }
        try {
            $token = $this->exchange($document, $input['code']);
            $result = DB::transaction(function () use ($account, $document, $token): array {
            $connectionInput = ['address' => $document['address'],
                'imap_upstream_id' => $document['imap_upstream_id'],
                'imap_username' => $document['address'], 'imap_auth' => $document['provider'],
                'imap_secret' => $token];
            if ($document['sending']) {
                $connectionInput += ['smtp_upstream_id' => $document['smtp_upstream_id'],
                    'smtp_username' => $document['address'], 'smtp_auth' => $document['provider'], 'smtp_secret' => $token];
            }
            if ($document['connection_id'] !== null) {
                $id = $document['connection_id'];
                $record = $document['mode'] === 'imap'
                    ? $this->connections->replaceImap($account, $id, $connectionInput)
                    : $this->connections->replace($account, $id, $connectionInput);
                if ($record === null) { throw new RuntimeException('Mailbox connection is unavailable.'); }
                $passwords = ['imap_password' => null, 'smtp_password' => null];
            } else {
                $created = $document['mode'] === 'imap'
                    ? $this->connections->createImap($account, $connectionInput)
                    : $this->connections->create($account, $connectionInput);
                $record = $created['record'];
                $id = $record->id;
                $passwords = $created;
            }
            if (! $this->tester->test($id)) {
                throw new RuntimeException('Mailbox connection test failed.');
            }
            $this->connections->markHealth($account, $id, true);
            $record = $this->connections->update($account, $id, true);
            $result = ['connection' => $this->resource($record),
                'imap_client_password' => $passwords['imap_password'],
                'smtp_client_password' => $passwords['smtp_password']];
            return $result;
            });
            DB::table('mailbox_oauth_transactions')->where('state_hash', $hash)->update([
                'status' => 'complete', 'result_ciphertext' => Crypt::encryptString(json_encode($result, JSON_THROW_ON_ERROR)),
                'updated_at' => now('UTC'),
            ]);
            return $result;
        } catch (Throwable) {
            throw new RuntimeException('Mailbox authorization could not be completed.');
        }
    }

    private function exchange(array $document, string $code): string
    {
        $settings = $this->settings($document['provider']);
        $url = $document['provider'] === 'google' ? 'https://oauth2.googleapis.com/token'
            : 'https://login.microsoftonline.com/'.$settings['tenant'].'/oauth2/v2.0/token';
        try {
            $response = Http::asForm()->acceptJson()->timeout(5)->post($url, [
                'client_id' => $settings['client_id'], 'client_secret' => $settings['client_secret'],
                'redirect_uri' => $document['callback_url'], 'grant_type' => 'authorization_code',
                'code' => $code, 'code_verifier' => $document['verifier'],
            ]);
        } catch (Throwable) { throw new RuntimeException('Mailbox provider is unavailable.'); }
        $body = $response->json();
        if (! $response->ok() || ! is_array($body) || ! is_string($body['refresh_token'] ?? null)
            || $body['refresh_token'] === '' || strlen($body['refresh_token']) > 8192
            || ! is_string($body['access_token'] ?? null) || $body['access_token'] === ''
            || strlen($body['access_token']) > 4096) {
            throw new RuntimeException('Mailbox provider did not return an offline grant.');
        }
        if ($document['provider'] === 'google') {
            try {
                $profile = Http::withToken($body['access_token'])->acceptJson()->timeout(5)
                    ->get('https://gmail.googleapis.com/gmail/v1/users/me/profile');
            } catch (Throwable) { throw new RuntimeException('Mailbox identity could not be verified.'); }
            $email = $profile->json('emailAddress');
            if (! $profile->ok() || ! is_string($email)
                || strcasecmp($email, $document['address']) !== 0) {
                throw new RuntimeException('Mailbox identity does not match.');
            }
        }
        return $body['refresh_token'];
    }

    private function settings(string $provider): array
    {
        $settings = config('gateway.email.oauth.'.$provider);
        $callback = config('gateway.email.oauth.callback_url');
        if (! is_array($settings) || ! is_string($settings['client_id'] ?? null) || $settings['client_id'] === ''
            || ! is_string($settings['client_secret'] ?? null) || $settings['client_secret'] === ''
            || ! is_string($callback) || strlen($callback) > 2048 || ! str_starts_with($callback, 'https://')
            || parse_url($callback, PHP_URL_HOST) === null
            || ($provider === 'microsoft' && preg_match('/\A(?:common|organizations|consumers|[a-fA-F0-9-]{36})\z/D',
                (string) ($settings['tenant'] ?? '')) !== 1)) {
            throw new RuntimeException('Mailbox provider is unavailable.');
        }
        return [...$settings, 'callback_url' => $callback];
    }

    private function resource(object $row): array
    {
        return ['id' => $row->id, 'account_id' => $row->account_id, 'address' => $row->address,
            'imap_upstream_id' => $row->imap_upstream_id, 'smtp_upstream_id' => $row->smtp_upstream_id,
            'imap_username' => $row->imap_username, 'smtp_username' => $row->smtp_username,
            'imap_auth' => $row->imap_auth, 'smtp_auth' => $row->smtp_auth,
            'enabled' => (bool) $row->enabled, 'health' => $row->health, 'tested_at' => $row->tested_at];
    }

    private static function base64url(string $bytes): string
    {
        return rtrim(strtr(base64_encode($bytes), '+/', '-_'), '=');
    }
}
