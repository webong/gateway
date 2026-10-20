<?php

declare(strict_types=1);

use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Email\MxResolver;
use Webong\Gateway\Email\VerificationRecordMissing;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

it('owns verified receiving domains and signed routes per account', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return $this->token === '' ? [] : ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $mx = new class implements MxResolver {
        public bool $ready = false;
        public bool $conflict = false;
        public function records(string $domain): array
        {
            if ($this->conflict) { return [
                ['host' => 'old.mail.test', 'priority' => 1], ['host' => 'mx.gateway.test', 'priority' => 10],
            ]; }
            return $this->ready ? [['host' => 'mx.gateway.test', 'priority' => 10]] : [];
        }
    };
    app()->instance(MxResolver::class, $mx);
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.gateway.test',
    ])->assertOk();
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one';

    $domain = $this->withToken($one['token'])->postJson($base.'/receiving-domains', ['domain' => 'acme.test'])
        ->assertCreated()->assertJsonPath('records.mx.value', 'mx.gateway.test')
        ->assertJsonPath('mx_status', 'pending')->json();
    $this->withToken($two['token'])->getJson($base.'/receiving-domains/'.$domain['id'])->assertUnauthorized();
    $this->withToken($two['token'])->getJson('https://localhost/email/mailbox-accounts/two/receiving-domains/'.$domain['id'])->assertNotFound();
    $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'mx', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/inbound',
    ])->assertConflict();

    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $mx->ready = true;
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$domain['id'].'/verify')->assertOk()
        ->assertJsonPath('verified_at', fn ($value) => is_string($value))->assertJsonPath('mx_status', 'ready');
    $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'acme.test'])->assertConflict();
    $mx->conflict = true;
    $this->withToken($one['token'])->getJson($base.'/receiving-domains/'.$domain['id'])
        ->assertOk()->assertJsonPath('mx_status', 'pending');
    $mx->conflict = false;
    $route = $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'mx', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/inbound',
    ])->assertCreated()->json();
    expect($route['address'])->toBe('support@acme.test');
    $this->withToken($one['token'])->getJson($base.'/receiving-routes/'.$route['id'])
        ->assertOk()->assertJsonMissingPath('webhook_secret');

    $mime = "From: client@outside.test\r\nTo: support@acme.test\r\nSubject: Help\r\n\r\nHello\r\n";
    $planner = app(ProtocolPlanner::class);
    $recipient = $planner->planEvent(new GatewayEvent(id: 'rcpt-1', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'],
        attributes: ['accepted_recipients' => '[]']));
    expect($recipient->action)->toBe(GatewayAction::ACCEPT);
    $decision = $planner->planEvent(new GatewayEvent(id: 'event-1', protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION, route: $route['address'],
        attributes: ['mail_from' => 'client@outside.test', 'recipients' => $route['address']], payload: $mime));
    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->payload)->toBe($mime)
        ->and($decision->deliveries[0]->target)->toBe('https://zorvia.example.test/inbound');
    $headers = $decision->deliveries[0]->headers;
    expect($headers['X-Gateway-Route-Id'][0])->toBe($route['id'])
        ->and($headers['X-Gateway-Event-Id'][0])->toBe('event-1');
    $signed = $route['id']."\n".'event-1'."\n".$headers['X-Gateway-Envelope'][0]."\n".hash('sha256', $mime);
    expect($headers['X-Gateway-Signature'][0])->toBe('sha256='.hash_hmac('sha256', $signed, $route['webhook_secret']));

    $this->withToken($one['token'])->patchJson($base.'/receiving-routes/'.$route['id'], ['enabled' => false])->assertOk();
    $paused = $planner->planEvent(new GatewayEvent(id: 'rcpt-2', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'], attributes: ['accepted_recipients' => '[]']));
    expect($paused->action)->toBe(GatewayAction::REJECT);
    $this->withToken($one['token'])->patchJson($base.'/receiving-routes/'.$route['id'], ['enabled' => true])->assertOk();
    $dns->token = '';
    $this->withToken('registry-secret')->postJson('/email/runtime-policy', ['verification_ttl_seconds' => 0])->assertCreated();
    expect(fn () => $planner->planEvent(new GatewayEvent(id: 'rcpt-revalidate', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'], attributes: ['accepted_recipients' => '[]'])))
        ->toThrow(VerificationRecordMissing::class);
    $unknown = $planner->planEvent(new GatewayEvent(id: 'rcpt-3', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: 'unknown@acme.test', attributes: ['accepted_recipients' => '[]']));
    expect($unknown->action)->toBe(GatewayAction::REJECT);
});

it('does not let an unverified account squat another account receiving domain', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return $this->token === '' ? [] : ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $first = $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/receiving-domains',
        ['domain' => 'acme.test'])->assertCreated()->json();
    $second = $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/receiving-domains',
        ['domain' => 'acme.test'])->assertCreated()->json();
    expect($first['id'])->not->toBe($second['id']);
    $dns->token = substr($second['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/receiving-domains/'.$second['id'].'/verify')
        ->assertOk()->assertJsonPath('domain', 'acme.test');
    $dns->token = substr($first['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/receiving-domains/'.$first['id'].'/verify')
        ->assertConflict();
});

it('creates opaque forwarding addresses under an operator-verified receiving domain', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return $this->token === '' ? [] : ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $forward = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'inbound.gateway.test'])
        ->assertCreated()->json();
    $dns->token = substr($forward['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$forward['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$forward['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.gateway.test', 'forwarding_domain' => 'inbound.gateway.test',
    ])->assertOk();
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one/receiving-routes';
    $input = ['mode' => 'forward', 'external_id' => 'zorvia-channel-1',
        'webhook_secret' => str_repeat('a', 64),
        'callback_url' => 'https://zorvia.example.test/inbound'];
    $first = $this->withToken($account['token'])->postJson($base, $input)->assertCreated()->json();
    $retried = $this->withToken($account['token'])->postJson($base, $input)->assertCreated()->json();
    $second = $this->withToken($account['token'])->postJson($base, [
        'mode' => 'forward', 'external_id' => 'zorvia-channel-2',
        'webhook_secret' => str_repeat('b', 64), 'callback_url' => 'https://zorvia.example.test/inbound',
    ])->assertCreated()->json();
    expect($first['address'])->toEndWith('@inbound.gateway.test')
        ->and($first['address'])->not->toBe($second['address'])
        ->and($retried['id'])->toBe($first['id']);
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'forward-1', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $first['address'], attributes: ['accepted_recipients' => '[]']));
    expect($decision->action)->toBe(GatewayAction::ACCEPT);
    $unknown = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'forward-unknown', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: 'random@inbound.gateway.test', attributes: ['accepted_recipients' => '[]']));
    expect($unknown->action)->toBe(GatewayAction::REJECT);
});

it('grants a private callback only when an operator approved that exact URL and network', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $forward = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'inbound.gateway.test'])
        ->assertCreated()->json();
    $dns->token = substr($forward['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$forward['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$forward['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.gateway.test', 'forwarding_domain' => 'inbound.gateway.test',
        'private_callbacks' => [['url' => 'https://zorvia:8443/gateway/mail', 'cidrs' => ['172.18.0.0/16']]],
    ])->assertOk();
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one/receiving-routes';
    $route = $this->withToken($account['token'])->postJson($base, [
        'mode' => 'forward', 'external_id' => 'local-compose-mail', 'webhook_secret' => str_repeat('c', 64),
        'network_scope' => 'private', 'callback_url' => 'https://zorvia:8443/gateway/mail',
    ])->assertCreated()->json();
    $this->withToken($account['token'])->postJson($base, [
        'mode' => 'forward', 'external_id' => 'wrong-url', 'webhook_secret' => str_repeat('d', 64),
        'network_scope' => 'private', 'callback_url' => 'https://zorvia:8443/admin',
    ])->assertConflict();
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'private-callback',
        protocol: Protocol::SMTP, kind: EventKind::TRANSACTION, route: $route['address'],
        attributes: ['mail_from' => 'sender@outside.test', 'recipients' => $route['address']], payload: 'Subject: hello'));
    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries[0]->attributes['private_callback_cidrs'])->toBe('172.18.0.0/16');
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.gateway.test', 'forwarding_domain' => 'inbound.gateway.test',
    ])->assertOk();
    $revoked = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'private-revoked',
        protocol: Protocol::SMTP, kind: EventKind::TRANSACTION, route: $route['address'],
        attributes: ['mail_from' => 'sender@outside.test', 'recipients' => $route['address']], payload: 'Subject: hello'));
    expect($revoked->action)->toBe(GatewayAction::REJECT)->and($revoked->statusCode)->toBe(451);
});
