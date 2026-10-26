<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocols\EventKind;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;

it('routes hosted mail from Planner resources while keeping provider tokens private', function (): void {
    config()->set('gateway.email.hosted_allowed_hosts', ['mail.provider.test']);
    $adapter = $this->withToken('registry-secret')->postJson('/email/hosted-adapters', [
        'name' => 'provider.primary', 'url' => 'https://mail.provider.test/accept',
        'bearer_token' => 'provider-secret',
    ])->assertCreated();
    expect($adapter->json())->not->toHaveKey('bearer_token');
    $this->getJson('/_internal/gateway/email/hosted-adapters/provider.primary')->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/email/hosted-adapters/provider.primary')->assertOk()
        ->assertJsonPath('bearer_token', 'provider-secret');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/webong/email/hosted-adapters/provider.primary')->assertOk()
        ->assertJsonPath('bearer_token', 'provider-secret');

    $txt = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $txt);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'hosted.test'])->assertCreated()->json();
    $txt->token = substr($domain['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $route = $this->withToken('registry-secret')->postJson('/email/hosted-routes', [
        'recipient' => 'support@hosted.test', 'account_id' => 'account-1',
        'adapter' => 'provider.primary', 'mailbox_key' => 'support',
    ])->assertCreated();
    $planner = app(ProtocolPlanner::class);
    $decision = $planner->planEvent(new GatewayEvent(id: 'hosted-resource', protocol: Protocol::SMTP,
        kind: EventKind::TRANSACTION, route: 'support@hosted.test',
        attributes: ['recipients' => 'support@hosted.test'], payload: "From: sender@outside.test\r\n\r\nbody\r\n"));
    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries[0]->adapter)->toBe('hosted.mail')
        ->and($decision->deliveries[0]->attributes['hosted_adapter_id'])->toBe('provider.primary');
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $adapter->headers->get('ETag')])
        ->putJson('/email/hosted-adapters/'.$adapter->json('id'), [
            'name' => 'provider.primary', 'url' => 'https://mail.provider.test/accept', 'enabled' => false,
        ])->assertOk();
    expect($planner->planEvent(new GatewayEvent(id: 'hosted-disabled', protocol: Protocol::SMTP,
        kind: EventKind::RECIPIENT, route: 'support@hosted.test'))->action)->not->toBe(GatewayAction::ACCEPT);
    expect($route->json('recipient'))->toBe('support@hosted.test');
});
