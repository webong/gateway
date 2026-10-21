<?php

declare(strict_types=1);

use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

it('keeps managed MX receiving and legacy route migration signed and account scoped', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings',
        ['mx_host' => 'mx.gateway.test'])->assertOk();
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one';
    $domain = $this->withToken($one['token'])->postJson($base.'/receiving-domains', ['domain' => 'managed.test'])->assertCreated()->json();
    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$domain['id'].'/verify')->assertOk();
    $input = ['mode' => 'managed', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/inbound', 'webhook_secret' => str_repeat('a', 64)];
    $this->withToken($two['token'])->postJson('https://localhost/email/mailbox-accounts/two/receiving-routes', $input)->assertConflict();
    $route = $this->withToken($one['token'])->postJson($base.'/receiving-routes', $input)->assertCreated()->json();
    expect($route['mode'])->toBe('managed')->and($route['status'])->toBe('active');
    $this->withToken($one['token'])->postJson($base.'/receiving-routes', $input)->assertCreated()->assertJsonPath('id', $route['id']);
    $this->withToken($one['token'])->getJson($base.'/receiving-routes/'.$route['id'])->assertJsonMissingPath('webhook_secret');
    $mime = "From: customer@outside.test\r\nTo: support@managed.test\r\n\r\nHello";
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'managed-event', protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION, route: $route['address'],
        attributes: ['mail_from' => 'customer@outside.test', 'recipients' => $route['address']], payload: $mime));
    expect($decision->action)->toBe(GatewayAction::DELIVER);
    $headers = $decision->deliveries[0]->headers;
    $signed = $route['id']."\nmanaged-event\n".$headers['X-Gateway-Envelope'][0]."\n".hash('sha256', $mime);
    expect($headers['X-Gateway-Signature'][0])->toBe('sha256='.hash_hmac('sha256', $signed, $input['webhook_secret']));
    $legacy = $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
        ...$input, 'mode' => 'mx', 'local_part' => 'legacy', 'webhook_secret' => str_repeat('b', 64),
    ])->assertCreated()->json();
    $url = $base.'/receiving-routes/'.$legacy['id'];
    $this->withToken($two['token'])->patchJson($url, ['mode' => 'managed'])->assertUnauthorized();
    $this->withToken($one['token'])->patchJson($url, ['mode' => 'managed'])->assertOk()->assertJsonPath('mode', 'managed');
    $this->withToken($one['token'])->patchJson($url, ['mode' => 'managed'])->assertOk()->assertJsonPath('id', $legacy['id']);
});

it('selects Worker ingress internally and only exposes generic DNS records to customers', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings', [
        'mx_host' => 'mx.gateway.test', 'managed_ingress' => ['adapter' => 'worker', 'records' => [
            'mx' => ['name' => '{domain}', 'value' => 'routing.example.test', 'priority' => 10],
            'cname' => ['name' => 'mail.{domain}', 'value' => 'routing.example.test'],
            'provider_txt' => ['type' => 'TXT', 'name' => '_mail.{domain}', 'value' => 'verify-domain'],
        ]],
    ])->assertOk();
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one';
    $domain = $this->withToken($one['token'])->postJson($base.'/receiving-domains', ['domain' => 'worker.test'])
        ->assertCreated()->assertJsonPath('records.cname.name', 'mail.worker.test')
        ->assertJsonPath('records.provider_txt.type', 'TXT')->json();
    expect(json_encode($domain))->not->toContain('worker', 'ingress_token', 'callback_url');
    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$domain['id'].'/verify')->assertOk();
    $route = $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'managed', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/inbound', 'webhook_secret' => str_repeat('c', 64),
    ])->assertCreated()->json();
    expect($route['mode'])->toBe('managed')->and($route['status'])->toBe('pending');
    $url = $base.'/receiving-routes/'.$route['id'].'/worker-ingress';
    $this->withToken($one['token'])->postJson($url, [])->assertNotFound();
    $this->withToken($one['token'])->getJson($url)->assertNotFound();
    $operator = 'https://localhost/email/operator/managed-worker-ingress/one/'.$route['id'].'/rotate';
    $this->withToken($one['token'])->postJson($operator, [])->assertUnauthorized();
    $token = $this->withToken('registry-secret')->postJson($operator, [])->assertCreated()->json('ingress_token');
    $this->withToken($one['token'])->getJson($base.'/receiving-routes/'.$route['id'])
        ->assertOk()->assertJsonPath('status', 'active')->assertJsonMissingPath('ingress_token');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/worker-ingress/'.$route['id'].'/authorize', [
            'token' => $token, 'mail_from' => 'sender@outside.test', 'recipient' => $route['address'],
            'event_id' => str_repeat('d', 64), 'message_sha256' => str_repeat('e', 64),
        ])->assertOk()->assertJsonPath('headers.X-Gateway-Route-Id.0', $route['id']);
    $smtp = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'worker-smtp', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'], attributes: ['accepted_recipients' => '[]']));
    expect($smtp->action)->toBe(GatewayAction::REJECT);
    $legacy = $this->withToken($one['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'worker', 'domain_id' => $domain['id'], 'local_part' => 'legacy',
        'callback_url' => 'https://zorvia.example.test/inbound', 'webhook_secret' => str_repeat('f', 64),
    ])->assertCreated()->json();
    $legacyUrl = $base.'/receiving-routes/'.$legacy['id'];
    $oldToken = $this->withToken($one['token'])->postJson($legacyUrl.'/worker-ingress', [])->assertCreated()->json('ingress_token');
    $this->withToken($one['token'])->patchJson($legacyUrl, ['mode' => 'managed'])
        ->assertOk()->assertJsonPath('id', $legacy['id'])->assertJsonPath('status', 'active');
    $this->withToken($one['token'])->postJson($legacyUrl.'/worker-ingress', [])->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson(
        '/_internal/gateway/worker-ingress/'.$legacy['id'].'/authorize', [
            'token' => $oldToken, 'mail_from' => 'sender@outside.test', 'recipient' => $legacy['address'],
            'event_id' => str_repeat('1', 64), 'message_sha256' => str_repeat('2', 64),
        ])->assertOk()->assertJsonPath('headers.X-Gateway-Route-Id.0', $legacy['id']);
});
