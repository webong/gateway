<?php

declare(strict_types=1);

use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Contract\ProtocolPlanner;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\PlannerResourceStore;

it('provisions a route-scoped Worker token and signs only its approved recipient', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'worker-owner'])
        ->assertCreated()->json();
    $other = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'other-owner'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/worker-owner';
    $domain = $this->withToken($account['token'])->postJson($base.'/receiving-domains', ['domain' => 'mail.yollof.com'])
        ->assertCreated()->json();
    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($account['token'])->postJson($base.'/receiving-domains/'.$domain['id'].'/verify')->assertOk();
    $route = $this->withToken($account['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'worker', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/webhooks/gateway-mail',
        'webhook_secret' => str_repeat('b', 64),
    ])->assertCreated()->assertJsonPath('address', 'support@mail.yollof.com')->json();
    $endpoint = $base.'/receiving-routes/'.$route['id'].'/worker-ingress';
    $this->withToken($other['token'])->postJson($endpoint, [])->assertUnauthorized();
    $token = $this->withToken($account['token'])->postJson($endpoint, [])->assertCreated()->json('ingress_token');
    expect($token)->toBeString()->toHaveLength(64);
    $this->withToken($account['token'])->getJson($endpoint)->assertOk()->assertJsonMissingPath('ingress_token');

    $body = ['token' => $token, 'mail_from' => 'sender@gmail.com', 'recipient' => 'support@mail.yollof.com',
        'event_id' => str_repeat('a', 64), 'message_sha256' => str_repeat('c', 64)];
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/worker-ingress/'.$route['id'].'/authorize', $body)->assertOk()
        ->assertJsonPath('callback_url', 'https://zorvia.example.test/webhooks/gateway-mail')
        ->assertJsonPath('headers.X-Gateway-Route-Id.0', $route['id']);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/worker-ingress/'.$route['id'].'/authorize', [...$body, 'recipient' => 'other@mail.yollof.com'])
        ->assertForbidden();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/worker-ingress/'.$route['id'].'/authorize', [...$body, 'token' => 'wrong'])
        ->assertForbidden();
});

it('switches an existing verified MX route to Worker ingress without changing its identity or callback', function (): void {
    $dns = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $dns);
    $this->withToken('registry-secret')->putJson('https://localhost/email/receiving-settings',
        ['mx_host' => 'mx.gateway.test'])->assertOk();
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'existing-owner'])
        ->assertCreated()->json();
    $other = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'different-owner'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/existing-owner';
    $domain = $this->withToken($account['token'])->postJson($base.'/receiving-domains', ['domain' => 'mail.yollof.com'])
        ->assertCreated()->json();
    $dns->token = substr($domain['records']['txt']['value'], strlen('gateway-verification='));
    $this->withToken($account['token'])->postJson($base.'/receiving-domains/'.$domain['id'].'/verify')->assertOk();
    $route = $this->withToken($account['token'])->postJson($base.'/receiving-routes', [
        'mode' => 'mx', 'domain_id' => $domain['id'], 'local_part' => 'support',
        'callback_url' => 'https://zorvia.example.test/webhooks/gateway-mail',
        'webhook_secret' => str_repeat('d', 64),
    ])->assertCreated()->json();
    $url = $base.'/receiving-routes/'.$route['id'];
    $this->withToken($other['token'])->patchJson($url, ['mode' => 'worker'])->assertUnauthorized();
    $this->withToken($account['token'])->patchJson($url, ['mode' => 'forward'])->assertUnprocessable();
    $switched = $this->withToken($account['token'])->patchJson($url, ['mode' => 'worker'])
        ->assertOk()->json();
    expect($switched['id'])->toBe($route['id'])
        ->and($switched['address'])->toBe($route['address'])
        ->and($switched['callback_url'])->toBe($route['callback_url'])
        ->and($switched['mode'])->toBe('worker');
    $smtp = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'smtp-after-switch', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'], attributes: ['accepted_recipients' => '[]']));
    expect($smtp->action)->toBe(GatewayAction::REJECT);
    $token = $this->withToken($account['token'])->postJson($url.'/worker-ingress', [])->assertCreated()->json('ingress_token');
    expect($token)->toHaveLength(64);
    $this->withToken($account['token'])->patchJson($url, ['mode' => 'worker'])->assertConflict();

    $this->withToken($other['token'])->patchJson($url, ['mode' => 'mx'])->assertUnauthorized();
    $restored = $this->withToken($account['token'])->patchJson($url, ['mode' => 'mx'])
        ->assertOk()->json();
    expect($restored['id'])->toBe($route['id'])
        ->and($restored['address'])->toBe($route['address'])
        ->and($restored['callback_url'])->toBe($route['callback_url'])
        ->and($restored['enabled'])->toBe($route['enabled'])
        ->and($restored['mode'])->toBe('mx');
    $this->withToken($account['token'])->getJson($url.'/worker-ingress')
        ->assertOk()->assertJsonPath('enabled', true);
    $smtp = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(id: 'smtp-after-restore', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: $route['address'], attributes: ['accepted_recipients' => '[]']));
    expect($smtp->action)->toBe(GatewayAction::ACCEPT);
    $this->withToken($account['token'])->patchJson($url, ['mode' => 'mx'])->assertConflict();
    $this->withToken($account['token'])->patchJson($url, ['mode' => 'worker'])->assertOk();
    $body = ['token' => $token, 'mail_from' => 'sender@gmail.com', 'recipient' => $route['address'],
        'event_id' => str_repeat('a', 64), 'message_sha256' => str_repeat('c', 64)];
    $decision = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/worker-ingress/'.$route['id'].'/authorize', $body)->assertOk()->json();
    $signed = $route['id']."\n".$body['event_id']."\n".$decision['headers']['X-Gateway-Envelope'][0]."\n".$body['message_sha256'];
    expect($decision['headers']['X-Gateway-Signature'][0])
        ->toBe('sha256='.hash_hmac('sha256', $signed, str_repeat('d', 64)));

    $resources = app(PlannerResourceStore::class);
    $storedDomain = $resources->find('email.receiving-domain', $domain['id']);
    $resources->replace('email.receiving-domain', $storedDomain->id, $storedDomain->key,
        $storedDomain->document, false, $storedDomain->revision);
    $this->withToken($account['token'])->patchJson($url, ['mode' => 'mx'])->assertConflict();
});
