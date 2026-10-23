<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Email\ApplicationMailboxGrantSource;
use Webong\Gateway\Email\ConnectedIMAPAuthenticator;
use Webong\Gateway\Email\ConfiguredApplicationMailboxGrantSource;
use Webong\Gateway\Email\CredentialScriptExecutor;
use Webong\Gateway\Email\CredentialScriptRegistry;
use Webong\Gateway\Email\EmailUpstreamCatalog;
use Webong\Gateway\Email\MailboxConnectionStore;
use Webong\Gateway\Email\MailboxCredentialSourceStore;
use Webong\Gateway\Email\MailboxPollStore;
use Webong\Gateway\Email\SpinnerCredentialScriptExecutor;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

function applicationMailboxInput(string $source): array
{
    return ['address' => 'mail@example.test', 'imap_upstream_id' => 'app-imap',
        'smtp_upstream_id' => 'app-smtp', 'imap_username' => 'mail@example.test',
        'smtp_username' => 'mail@example.test', 'imap_auth' => 'xoauth2',
        'smtp_auth' => 'xoauth2', 'credential_mode' => 'application_managed',
        'credential_source_id' => $source];
}

function applicationMailboxUpstreams(): void
{
    $catalog = app(EmailUpstreamCatalog::class);
    $catalog->create(['protocol' => 'imap', 'name' => 'app-imap',
        'address' => 'imap.example.test:993', 'server_name' => 'imap.example.test']);
    $catalog->create(['protocol' => 'smtp', 'name' => 'app-smtp',
        'address' => 'smtp.example.test:465', 'server_name' => 'smtp.example.test',
        'tls_mode' => 'implicit']);
}

it('stores generic credential scripts and exposes only a declarative request capability', function (): void {
    $token = app(AccountStore::class)->create('app-one');
    $executor = new class implements CredentialScriptExecutor {
        public array $events = [];

        public function execute(string $source, array $event): Delivery
        {
            $this->events[] = compact('source', 'event');
            return new Delivery($event['source_url'], method: 'PATCH',
                headers: ['Content-Type' => ['application/json']], body: '{"context":true}');
        }
    };
    app(CredentialScriptRegistry::class)->register('testscript', $executor);
    $payload = ['name' => 'scripted', 'adapter' => 'script', 'language' => 'testscript',
        'source' => 'forward source', 'url' => 'https://credentials.example.com/setup',
        'authorization' => str_repeat('s', 48)];
    $id = $this->withToken($token)
        ->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources', $payload)
        ->assertCreated()->assertJsonPath('adapter', 'script')->assertJsonPath('language', 'testscript')
        ->assertJsonPath('source', 'forward source')->assertDontSee($payload['authorization'])->json('id');
    $stored = app(MailboxCredentialSourceStore::class)->active('app-one', $id);
    expect($stored)->toMatchArray(['adapter' => 'script', 'language' => 'testscript',
        'source' => 'forward source', 'url' => $payload['url']]);

    $delivery = app(CredentialScriptRegistry::class)->execute('testscript', $stored['source'], [
        'source_url' => $stored['url'], 'protocol' => 'imap',
    ]);
    expect($delivery->method)->toBe('PATCH')
        ->and($executor->events[0]['event'])->not->toHaveKey('authorization');
    $this->withToken($token)->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
        [...$payload, 'name' => 'unsupported', 'language' => 'missing'])->assertUnprocessable();
});

it('executes Go credential scripts through the authenticated private admin contract', function (): void {
    config()->set('gateway.internal_token', 'private-token');
    config()->set('gateway.delivery_admin_url', 'http://gateway-admin.test');
    Http::fake(['http://gateway-admin.test/automation/credential-hook' => Http::response([
        'delivery' => ['url' => 'https://application.example.test/setup', 'method' => 'PATCH',
            'headers' => ['Content-Type' => ['application/json']], 'body' => '{"context":true}'],
    ])]);

    $delivery = (new SpinnerCredentialScriptExecutor(app(DeliveryAdminClient::class), 'javascript'))
        ->execute('gateway.forward({});', ['protocol' => 'imap']);

    expect($delivery->method)->toBe('PATCH')->and($delivery->body)->toBe('{"context":true}');
    Http::assertSent(fn ($request): bool => $request->url() === 'http://gateway-admin.test/automation/credential-hook'
        && $request->hasHeader('Authorization', 'Bearer private-token')
        && $request['language'] === 'javascript'
        && $request['event'] === ['protocol' => 'imap']);
});

it('registers an account-owned HTTPS credential source without exposing its bearer token', function (): void {
    $accounts = app(AccountStore::class);
    $one = $accounts->create('app-one');
    $two = $accounts->create('app-two');
    $input = ['name' => 'primary', 'url' => 'https://credentials.example.com/gateway/grants',
        'authorization' => str_repeat('s', 48)];
    $this->withToken($one)->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
        $input)->assertCreated()->assertJsonPath('name', 'primary')->assertJsonPath('adapter', 'http')
        ->assertDontSee($input['authorization']);
    $id = DB::table('configurations')->where('kind', 'email.credential-source')->value('id');
    expect(DB::table('configurations')->where('id', $id)->value('document'))->not->toContain($input['authorization'])
        ->and(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext'))
        ->not->toContain($input['authorization']);
    $rotated = [...$input, 'authorization' => str_repeat('n', 48)];
    $this->withToken($one)->putJson('https://localhost/email/mailbox-accounts/app-one/credential-sources/'.$id,
        $rotated)->assertOk()->assertJsonPath('id', $id)->assertDontSee($rotated['authorization']);
    expect(Crypt::decryptString(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext')))
        ->toBe($rotated['authorization']);
    $this->withToken($two)->getJson('https://localhost/email/mailbox-accounts/app-one/credential-sources')
        ->assertUnauthorized();
    $this->withToken($two)->deleteJson('https://localhost/email/mailbox-accounts/app-two/credential-sources/'.$id)
        ->assertNotFound();
    foreach (['https://127.0.0.1/grants', 'http://credentials.example.com/grants',
        'https://credentials.example.com:8443/grants', 'https://user:pass@credentials.example.com/grants'] as $url) {
        $this->withToken($one)->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
            ['name' => 'bad', 'url' => $url, 'authorization' => str_repeat('s', 48)])->assertUnprocessable();
    }
});

it('selects an installed client adapter per source and contains its failures', function (): void {
    $token = app(AccountStore::class)->create('app-one');
    app(AccountStore::class)->create('app-two');
    $client = new class implements ApplicationMailboxGrantSource {
        public array $calls = [];
        public bool $fail = false;

        public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
        {
            $source = app(MailboxCredentialSourceStore::class)->active($account, $sourceId);
            $this->calls[] = compact('sourceId', 'account', 'connection', 'protocol', 'address', 'source');
            if ($this->fail) { throw new RuntimeException('remote body with secret'); }
            return ['account_id' => $account, 'connection_id' => $connection, 'address' => $address,
                'access_token' => 'transient', 'expires_at' => time() + 600,
                'capabilities' => [$protocol]];
        }
    };
    config()->set('gateway.email.credential_source_adapters.client', $client::class);
    app()->instance($client::class, $client);
    $authorization = str_repeat('s', 48);
    $source = $this->withToken($token)
        ->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
            ['name' => 'client', 'adapter' => 'client',
                'url' => 'https://client.example.com/opaque/setup',
                'authorization' => $authorization])
        ->assertCreated()->assertJsonPath('adapter', 'client')
        ->assertDontSee($authorization)->json('id');
    $this->withToken($token)->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
        ['name' => 'unknown', 'adapter' => 'unknown', 'url' => 'https://client.example.com/setup',
            'authorization' => $authorization])->assertUnprocessable();
    $resolver = app(ApplicationMailboxGrantSource::class);
    expect($resolver)->toBeInstanceOf(ConfiguredApplicationMailboxGrantSource::class);
    $grant = $resolver->resolve($source, 'app-one', 'connection-one', 'smtp', 'mail@example.test');
    expect($grant['access_token'])->toBe('transient')
        ->and($client->calls[0]['source'])->toBe([
            'url' => 'https://client.example.com/opaque/setup', 'adapter' => 'client',
            'authorization' => $authorization])
        ->and($client->calls[0]['protocol'])->toBe('smtp');
    applicationMailboxUpstreams();
    $connection = $this->withToken($token)
        ->postJson('https://localhost/email/mailbox-accounts/app-one/connections',
            applicationMailboxInput($source))->assertCreated()->json('connection.id');
    expect(app(MailboxConnectionStore::class)->resolve($connection, 'smtp', allowDisabled: true)['secret'])
        ->toBe('transient');
    expect(fn () => $resolver->resolve($source, 'app-two', 'connection-one', 'smtp', 'mail@example.test'))
        ->toThrow(RuntimeException::class, 'Mailbox credential source is unavailable.');
    expect(count($client->calls))->toBe(2);
    $client->fail = true;
    try { $resolver->resolve($source, 'app-one', 'connection-one', 'smtp', 'mail@example.test'); }
    catch (RuntimeException $error) {
        expect($error->getMessage())->toBe('Mailbox credential source is unavailable.')
            ->not->toContain('remote body with secret');
        $failed = true;
    }
    expect($failed ?? false)->toBeTrue();
    config()->set('gateway.email.credential_source_adapters.client', null);
    expect(fn () => $resolver->resolve($source, 'app-one', 'connection-one', 'smtp', 'mail@example.test'))
        ->toThrow(RuntimeException::class, 'Mailbox credential source is unavailable.');
    expect(app(MailboxCredentialSourceStore::class)->active('app-two', $source))->toBeNull();
});

it('uses only transient scoped grants for application-managed IMAP and SMTP', function (): void {
    applicationMailboxUpstreams();
    $one = app(AccountStore::class)->create('app-one');
    app(AccountStore::class)->create('app-two');
    $source = $this->withToken($one)->postJson('https://localhost/email/mailbox-accounts/app-one/credential-sources',
        ['name' => 'primary', 'url' => 'https://credentials.example.com/grants',
            'authorization' => str_repeat('s', 48)])->assertCreated()->json('id');
    $this->withToken(app(AccountStore::class)->create('app-three'))
        ->postJson('https://localhost/email/mailbox-accounts/app-three/connections',
            applicationMailboxInput($source))->assertUnprocessable();
    $grants = new class implements ApplicationMailboxGrantSource {
        public string $token = 'access-one';
        public array $overrides = [];
        public array $calls = [];

        public function resolve(string $sourceId, string $account, string $connection, string $protocol, string $address): array
        {
            $this->calls[] = compact('sourceId', 'account', 'connection', 'protocol', 'address');
            return array_replace(['account_id' => $account, 'connection_id' => $connection,
                'address' => $address, 'access_token' => $this->token,
                'expires_at' => time() + 600, 'capabilities' => ['imap', 'smtp']], $this->overrides);
        }
    };
    app()->instance(ApplicationMailboxGrantSource::class, $grants);
    $input = applicationMailboxInput($source);
    $created = $this->withToken($one)->postJson('https://localhost/email/mailbox-accounts/app-one/connections',
        $input)->assertCreated()->assertJsonPath('connection.credential_mode', 'application_managed')->json();
    $id = $created['connection']['id'];
    $store = app(MailboxConnectionStore::class);
    expect($created['imap_client_password'])->toBeString()
        ->and($created['smtp_client_password'])->toBeString()
        ->and($store->resolve($id, 'imap', allowDisabled: true)['secret'])->toBe('access-one')
        ->and($store->resolve($id, 'smtp', allowDisabled: true)['mechanism'])->toBe('XOAUTH2');
    $grants->token = 'access-two';
    expect($store->resolve($id, 'imap', allowDisabled: true)['secret'])->toBe('access-two')
        ->and(count($grants->calls))->toBe(3)
        ->and($grants->calls[0]['account'])->toBe('app-one')
        ->and($grants->calls[0]['sourceId'])->toBe($source);
    $stored = Crypt::decryptString(DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext'));
    expect($stored)->not->toContain('access-one', 'access-two', 'refresh_token')
        ->and(json_decode($stored, true)['imap_secret'])->toBeNull()
        ->and(json_decode($stored, true)['smtp_secret'])->toBeNull();

    $store->markHealth('app-one', $id, true);
    $store->update('app-one', $id, true);
    $imap = app(ConnectedIMAPAuthenticator::class)->authorize(new GatewayEvent(
        id: 'app-imap', protocol: Protocol::IMAP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $created['imap_client_password']],
    ));
    expect($imap?->upstreamPassword)->toBe('access-two');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertOk()
        ->assertJsonPath('mechanism', 'XOAUTH2')->assertJsonPath('secret', 'access-two');

    $polls = app(MailboxPollStore::class);
    $poll = $polls->configure('app-one', $id, ['callback_url' => 'https://receiver.example.com/mail',
        'webhook_secret' => str_repeat('a', 64), 'enabled' => true]);
    $claim = $polls->claim($poll->id);
    expect($claim['secret'])->toBe('access-two');
    $polls->release($poll->id, $claim['lease_token']);
    $grants->overrides = ['expires_at' => time() + 90];
    expect(fn () => $polls->claim($poll->id))->toThrow(RuntimeException::class)
        ->and(DB::table('resource_checkpoints')->where('resource_id', $poll->id)->value('lease_token'))->toBeNull();
    $grants->overrides = [];
    $claim = $polls->claim($poll->id);
    expect($claim['secret'])->toBe('access-two');
    $polls->release($poll->id, $claim['lease_token']);

    foreach ([['account_id' => 'app-two'], ['connection_id' => '00000000-0000-0000-0000-000000000000'],
        ['address' => 'other@example.test'], ['capabilities' => ['smtp']], ['expires_at' => time() + 5],
        ['access_token' => "invalid\naccess"]] as $denial) {
        $grants->overrides = $denial;
        expect(fn () => $store->resolve($id, 'imap'))->toThrow(RuntimeException::class);
    }
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/mailbox-connections/'.$id.'/smtp')->assertServiceUnavailable()
        ->assertDontSee('access-two');
    expect(app(ConnectedIMAPAuthenticator::class)->authorize(new GatewayEvent(
        id: 'denied-imap', protocol: Protocol::IMAP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $created['imap_client_password']],
    )))->toBeNull();
    $grants->overrides = [];
    $this->withToken($one)->deleteJson('https://localhost/email/mailbox-accounts/app-one/credential-sources/'.$source)
        ->assertOk();
    expect(fn () => $store->resolve($id, 'imap'))->toThrow(RuntimeException::class);
});
