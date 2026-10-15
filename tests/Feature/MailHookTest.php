<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Protocol;

it('routes verified MX mail to subscribed webhooks with raw MIME and envelope metadata', function (): void {
    $resolver = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return str_starts_with($name, '_gateway-verify.') ? ['gateway-verification='.$this->token] : []; }
    };
    app()->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'mailhook.test'])->assertCreated()->json();
    $resolver->token = substr($domain['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $hook = $this->withToken('registry-secret')->postJson('/email/hooks', ['domain' => 'mailhook.test'])->assertCreated()->json();
    $this->withToken('registry-secret')->postJson($hook['subscriptions_url'], [
        'subscriber_id' => 'workspace-42', 'subscription_id' => 'workspace-42-mail',
        'type' => 'relay', 'webhook_group' => 'mail', 'routing_scope' => 'smtp',
        'routing_key' => 'recipient', 'url' => 'https://subscriber.example.test/mail',
    ])->assertCreated();

    $binding = app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'mail-path-1', method: 'POST', host: 'mx.mailhook.test',
        path: 'support@mailhook.test', rawQuery: '', headers: [], body: '',
        scheme: 'smtp', protocol: 'smtp', event: 'transaction',
    ));
    expect($binding?->endpointKey)->toBe($hook['endpoint_key']);

    $mime = "From: Customer <customer@outside.test>\r\nTo: support@mailhook.test\r\nSubject: Hello\r\n\r\nBody\r\n";
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'mail-event-1', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'support@mailhook.test', host: 'mx.mailhook.test',
        attributes: ['mail_from' => 'customer@outside.test', 'recipients' => 'support@mailhook.test'],
        payload: $mime,
    ));
    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->payload)->toBe($mime)
        ->and($decision->deliveries[0]->target)->toBe('https://subscriber.example.test/mail')
        ->and($decision->deliveries[0]->headers['Content-Type'])->toBe(['message/rfc822']);
    $envelope = json_decode(base64_decode($decision->deliveries[0]->headers['X-Gateway-Envelope'][0]), true);
    expect($envelope)->toBe(['mail_from' => 'customer@outside.test', 'recipients' => ['support@mailhook.test']]);

    $extra = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'mail-event-2', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'sales@mailhook.test', attributes: ['mail_from' => 'customer@outside.test',
            'accepted_recipients' => '["support@mailhook.test"]'],
    ));
    expect($extra->action)->toBe(GatewayAction::REJECT)->and($extra->statusCode)->toBe(550);

    $this->withToken('registry-secret')->patchJson('/email/hooks/'.$hook['id'], ['active' => false])->assertOk();
    expect(app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'mail-path-2', method: 'POST', host: 'mx.mailhook.test',
        path: 'support@mailhook.test', rawQuery: '', headers: [], body: '',
        scheme: 'smtp', protocol: 'smtp', event: 'transaction',
    ))?->endpointKey)->not->toBe($hook['endpoint_key']);
});

it('routes a single mailbox to a hook while another address keeps alias forwarding', function (): void {
    $resolver = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return str_starts_with($name, '_gateway-verify.') ? ['gateway-verification='.$this->token] : []; }
    };
    app()->instance(TxtResolver::class, $resolver);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'mixed.test'])->assertCreated()->json();
    $resolver->token = substr($domain['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/aliases', [
        'local_part' => 'sales', 'recipients' => ['sales@external.test'],
    ])->assertCreated();
    $hook = $this->withToken('registry-secret')->postJson('/email/hooks', [
        'domain' => 'mixed.test', 'local_part' => 'support',
    ])->assertCreated()->json();

    expect($hook['local_part'])->toBe('support');
    $binding = app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'mixed-1', method: 'POST', host: 'mx.mixed.test', path: 'support@mixed.test',
        rawQuery: '', headers: [], body: '', scheme: 'smtp', protocol: 'smtp', event: 'transaction',
    ));
    expect($binding?->endpointKey)->toBe($hook['endpoint_key']);
    $aliasBinding = app(PathResolver::class)->resolve(new IngressRequest(
        deliveryId: 'mixed-2', method: 'POST', host: 'mx.mixed.test', path: 'sales@mixed.test',
        rawQuery: '', headers: [], body: '', scheme: 'smtp', protocol: 'smtp', event: 'transaction',
    ));
    expect($aliasBinding?->endpointKey)->not->toBe($hook['endpoint_key']);
    $decision = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'mixed-event', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'sales@mixed.test', attributes: ['mail_from' => 'sender@outside.test', 'recipients' => 'sales@mixed.test'],
        payload: "From: sender@outside.test\r\nTo: sales@mixed.test\r\n\r\nhello\r\n",
    ));
    expect($decision->action)->toBe(GatewayAction::SUBMIT);

    $this->withToken('registry-secret')->postJson('/email/hooks', ['domain' => 'mixed.test'])->assertCreated();
    $afterWildcard = app(ProtocolPlanner::class)->planEvent(new GatewayEvent(
        id: 'mixed-event-2', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'sales@mixed.test', attributes: ['mail_from' => 'sender@outside.test', 'recipients' => 'sales@mixed.test'],
        payload: "From: sender@outside.test\r\nTo: sales@mixed.test\r\n\r\nhello\r\n",
    ));
    expect($afterWildcard->action)->toBe(GatewayAction::SUBMIT);
});
