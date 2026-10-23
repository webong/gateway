<?php

declare(strict_types=1);

use Illuminate\Http\Client\Request as ClientRequest;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\OutboundTransportStore;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

it('provides a Gateway-managed sending route without an external SMTP provider', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public array $published = [];
        public function records(string $name): array
        {
            return $this->published[$name] ?? ($this->token === '' ? [] : ['gateway-verification='.$this->token]);
        }
    };
    app()->instance(TxtResolver::class, $dns);
    config()->set('gateway.email.managed_dkim_signing_enabled', true);
    config()->set('gateway.email.managed_spf_value', 'v=spf1 include:_spf.operator.test -all');
    $account = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'zorvia'])
        ->assertCreated()->json();
    $other = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'other'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/zorvia';
    $domain = $this->withToken($account['token'])->postJson($base.'/sending-domains', ['domain' => 'acme.test'])
        ->assertCreated()->assertJsonPath('dns_status.ownership', 'pending')
        ->assertJsonPath('dns_status.spf', 'pending')->json();
    $this->withToken($other['token'])->getJson($base.'/sending-domains/'.$domain['id'])->assertUnauthorized();
    $this->withToken($other['token'])->getJson('https://localhost/email/mailbox-accounts/other/sending-domains/'.$domain['id'])
        ->assertNotFound();
    $this->withToken($account['token'])->postJson($base.'/sending-routes', [
        'domain_id' => $domain['id'], 'local_part' => 'support',
    ])->assertConflict();
    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $verified = $this->withToken($account['token'])->postJson($base.'/sending-domains/'.$domain['id'].'/verify')
        ->assertOk()->assertJsonPath('dns_status.ownership', 'verified')
        ->assertJsonPath('dns_status.dkim', 'pending')->json();
    expect($verified['records']['dkim']['name'])->toBe('gateway1._domainkey.acme.test')
        ->and($verified['records']['dkim']['value'])->toStartWith('v=DKIM1; k=rsa; p=')
        ->and($verified['records']['spf']['value'])->toBe('v=spf1 include:_spf.operator.test -all');
    $this->withToken($account['token'])->postJson($base.'/sending-routes', [
        'domain_id' => $domain['id'], 'local_part' => 'support',
    ])->assertConflict();
    $dns->published[$verified['records']['dkim']['name']] = [$verified['records']['dkim']['value']];
    $dns->published[$verified['records']['spf']['name']] = [$verified['records']['spf']['value']];
    $this->withToken($account['token'])->getJson($base.'/sending-domains/'.$domain['id'])
        ->assertOk()->assertJsonPath('dns_status.sending', 'ready');
    $created = $this->withToken($account['token'])->postJson($base.'/sending-routes', [
        'domain_id' => $domain['id'], 'local_part' => 'support', 'enabled' => false,
    ])->assertCreated()->assertJsonPath('address', 'support@acme.test')
        ->assertJsonPath('enabled', false)->assertJsonPath('status', 'disabled')
        ->assertJsonMissingPath('smtp_upstream_id')->json();
    $id = $created['id'];
    $password = $created['smtp_client_password'];
    $this->withToken($account['token'])->getJson($base.'/sending-routes/'.$id)
        ->assertOk()->assertJsonMissingPath('smtp_client_password')->assertJsonMissingPath('password_hash');
    $this->withToken($other['token'])->getJson('https://localhost/email/mailbox-accounts/other/sending-routes/'.$id)
        ->assertNotFound();

    $fallbackRoute = $this->withToken($account['token'])->postJson($base.'/sending-routes', [
        'domain_id' => $domain['id'], 'local_part' => 'billing', 'enabled' => false,
    ])->assertCreated()->assertJsonPath('enabled', false)->json();
    $unreadyRoute = $this->withToken($account['token'])->postJson($base.'/sending-routes', [
        'domain_id' => $domain['id'], 'local_part' => 'unready', 'enabled' => false,
    ])->assertCreated()->assertJsonPath('enabled', false)->json();
    $domainTransport = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'domain-smtp', 'type' => 'smtp', 'address' => 'smtp.domain.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true,
        'authorized_domains' => ['acme.test'],
        'domain_readiness' => ['acme.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $exactTransport = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'support-smtp', 'type' => 'smtp', 'address' => 'smtp.support.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true,
        'authorized_domains' => ['acme.test'],
        'domain_readiness' => ['acme.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $domainPool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'domain-pool',
        'members' => [['transport_id' => $domainTransport['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $exactPool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'support-pool',
        'members' => [['transport_id' => $exactTransport['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'zorvia', 'scope' => 'domain', 'sender_id' => $domain['id'], 'pool_id' => $domainPool['id'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'zorvia', 'scope' => 'managed_route', 'sender_id' => $id, 'pool_id' => $exactPool['id'],
    ])->assertCreated();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'other', 'scope' => 'managed_route', 'sender_id' => $id, 'pool_id' => $exactPool['id'],
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'zorvia', 'scope' => 'shared_route', 'sender_id' => $id, 'pool_id' => $exactPool['id'],
    ])->assertUnprocessable();
    unset($dns->published[$verified['records']['dkim']['name']]);
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'zorvia', 'scope' => 'managed_route',
        'sender_id' => $unreadyRoute['id'], 'pool_id' => $exactPool['id'],
    ])->assertUnprocessable();
    $dns->published[$verified['records']['dkim']['name']] = [$verified['records']['dkim']['value']];
    expect(app(OutboundTransportStore::class)->senderBinding('zorvia', $id))->toBeNull();
    $this->withToken($account['token'])->patchJson($base.'/sending-routes/'.$id, ['enabled' => true])
        ->assertOk()->assertJsonPath('status', 'active');
    $this->withToken($account['token'])->patchJson($base.'/sending-routes/'.$fallbackRoute['id'], ['enabled' => true])
        ->assertOk()->assertJsonPath('status', 'active');
    expect(app(OutboundTransportStore::class)->senderBinding('zorvia', $id)['pool_id'])->toBe($exactPool['id'])
        ->and(app(OutboundTransportStore::class)->senderBinding('zorvia', $fallbackRoute['id'])['pool_id'])
        ->toBe($domainPool['id']);

    $authenticate = static fn (string $password): GatewayEvent => new GatewayEvent(
        id: bin2hex(random_bytes(8)), protocol: Protocol::SMTP, kind: EventKind::AUTHENTICATE,
        route: $id, attributes: ['username' => $id, 'password' => $password],
    );
    $planner = app(ProtocolPlanner::class);
    $grant = $planner->planEvent($authenticate($password));
    expect($grant->action)->toBe(GatewayAction::ACCEPT)
        ->and($grant->metadata['auth_identity'])->toBe('zorvia:'.$id)
        ->and($grant->metadata['authorized_mail_from'])->toBe('["support@acme.test"]')
        ->and($grant->metadata)->not->toHaveKey('mailbox_connection_id');
    unset($dns->published[$verified['records']['dkim']['name']]);
    expect($planner->planEvent($authenticate($password))->action)->toBe(GatewayAction::REJECT);
    $dns->published[$verified['records']['dkim']['name']] = [$verified['records']['dkim']['value']];

    app(EmailRuntimePolicyStore::class)->create(['http_submission_enabled' => true]);
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');
    Http::fake(fn (ClientRequest $request) => Http::response(['id' => $request['receipt_id']], 202));
    $headers = ['Authorization' => 'Basic '.base64_encode($id.':'.$password), 'Idempotency-Key' => 'managed-1'];
    $payload = ['mail_from' => 'support@acme.test', 'recipients' => ['client@outside.test'],
        'message_base64' => base64_encode("From: support@acme.test\r\nSubject: hello\r\n\r\nhello")];
    $this->withHeaders($headers)->postJson('https://localhost/email/submissions', $payload)->assertAccepted();
    Http::assertSent(fn (ClientRequest $request): bool => ! isset($request['mailbox_connection_id'])
        && $request['mail_from'] === 'support@acme.test');
    $this->withHeaders([...$headers, 'Idempotency-Key' => 'managed-2'])->postJson('https://localhost/email/submissions', [
        ...$payload, 'mail_from' => 'other@acme.test',
    ])->assertUnauthorized();

    $rotated = $this->withToken($account['token'])->postJson($base.'/sending-routes/'.$id.'/rotate')->assertOk()->json();
    expect($planner->planEvent($authenticate($password))->action)->toBe(GatewayAction::REJECT)
        ->and($planner->planEvent($authenticate($rotated['smtp_client_password']))->action)->toBe(GatewayAction::ACCEPT);
    $dns->token = '';
    $policy = app(EmailRuntimePolicyStore::class);
    $policy->replace(['http_submission_enabled' => true, 'verification_ttl_seconds' => 0], $policy->find()->revision);
    expect($planner->planEvent($authenticate($rotated['smtp_client_password']))->action)->toBe(GatewayAction::REJECT);
    $this->withToken($account['token'])->patchJson($base.'/sending-routes/'.$id, ['enabled' => false])->assertOk();
    expect($planner->planEvent($authenticate($rotated['smtp_client_password']))->action)->toBe(GatewayAction::REJECT);
    unset($dns->published[$verified['records']['dkim']['name']]);
    $this->withToken($account['token'])->patchJson($base.'/sending-routes/'.$id, ['enabled' => true])->assertConflict();
    $this->withToken($account['token'])->deleteJson($base.'/sending-domains/'.$domain['id'])->assertConflict();
    $this->withToken($account['token'])->deleteJson($base.'/sending-routes/'.$id)->assertOk();
    $this->withToken($account['token'])->deleteJson($base.'/sending-routes/'.$fallbackRoute['id'])->assertOk();
    $this->withToken($account['token'])->deleteJson($base.'/sending-routes/'.$unreadyRoute['id'])->assertOk();
    $this->withToken($account['token'])->deleteJson($base.'/sending-domains/'.$domain['id'])->assertOk();
});

it('does not let an unverified sending-domain claim block another account', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $one = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'two'])->assertCreated()->json();
    $first = $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/sending-domains',
        ['domain' => 'acme.test'])->assertCreated()->json();
    $second = $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/sending-domains',
        ['domain' => 'acme.test'])->assertCreated()->json();
    $dns->token = substr($second['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/sending-domains/'.$second['id'].'/verify')
        ->assertOk();
    $dns->token = substr($first['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/sending-domains/'.$first['id'].'/verify')
        ->assertConflict();
});

it('shares one ownership TXT challenge across receiving and sending for the same account', function (): void {
    $first = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'first'])
        ->assertCreated()->json();
    $second = $this->withToken('registry-secret')->postJson('https://localhost/accounts', ['id' => 'second'])
        ->assertCreated()->json();
    $firstBase = 'https://localhost/email/mailbox-accounts/first';
    $secondBase = 'https://localhost/email/mailbox-accounts/second';

    $firstReceive = $this->withToken($first['token'])->postJson($firstBase.'/receiving-domains',
        ['domain' => 'first.test'])->assertCreated()->json();
    $firstSend = $this->withToken($first['token'])->postJson($firstBase.'/sending-domains',
        ['domain' => 'first.test'])->assertCreated()->json();
    expect($firstSend['records']['txt'])->toBe($firstReceive['records']['txt']);

    $secondSend = $this->withToken($second['token'])->postJson($secondBase.'/sending-domains',
        ['domain' => 'second.test'])->assertCreated()->json();
    $secondReceive = $this->withToken($second['token'])->postJson($secondBase.'/receiving-domains',
        ['domain' => 'second.test'])->assertCreated()->json();
    expect($secondReceive['records']['txt'])->toBe($secondSend['records']['txt']);
    $competing = $this->withToken($second['token'])->postJson($secondBase.'/sending-domains',
        ['domain' => 'first.test'])->assertCreated()->json();
    expect($competing['records']['txt']['value'])->not->toBe($firstSend['records']['txt']['value']);

    $kinds = $this->withToken('registry-secret')->getJson('/configurations/kinds')->assertOk()->json('data');
    expect($kinds)->toContain('email.sending-domain', 'email.sending-route');
    $this->withToken('registry-secret')->getJson('/configurations/email.sending-domain/'.$firstSend['id'].'/revisions')
        ->assertOk()->assertJsonPath('data.0.operation', 'created');
});
