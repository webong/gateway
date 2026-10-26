<?php

declare(strict_types=1);

use Webong\Gateway\Email\OutboundTransportStore;
use Webong\Gateway\Email\EmailDkimKeyStore;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Resources\PlannerResourceStore;

beforeEach(function (): void {
    config()->set('gateway.email.managed_dkim_signing_enabled', true);
    app(EmailDkimKeyStore::class)->ensureGenerated('operator.test', 'gateway1');
    app()->instance(TxtResolver::class, new class implements TxtResolver {
        public function records(string $name): array
        {
            $record = app(EmailDkimKeyStore::class)->publicRecord('operator.test');
            return $record !== null && $name === $record['name'] ? [$record['value']] : [];
        }
    });
});

it('keeps SMTP credentials private and pins weighted outbound plans per account', function (): void {
    config()->set('gateway.email.shared_from_address', 'support@operator.test');
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])->assertCreated()->json();
    $sender = $this->withToken($one['token'])->postJson('https://localhost/email/mailbox-accounts/one/sending-routes', ['mode' => 'shared'])
        ->assertCreated()->json();
    $secret = 'very-private-smtp-password';
    $first = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'primary', 'type' => 'smtp', 'address' => 'smtp.primary.test:587',
        'tls_mode' => 'starttls', 'username' => 'gateway', 'password' => $secret,
        'healthy' => true,
        'authorized_domains' => ['operator.test'],
        'domain_readiness' => ['operator.test' => ['spf' => 'ready', 'dkim' => 'ready']],
        'rate_limit_per_minute' => 1,
    ])->assertCreated()->assertJsonMissingPath('password')->assertJsonMissingPath('credential_ref')->json();
    $second = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'backup', 'type' => 'smtp', 'address' => 'smtp.backup.test:587',
        'tls_mode' => 'starttls', 'username' => 'gateway', 'password' => 'backup-password',
        'healthy' => true,
        'authorized_domains' => ['operator.test'],
        'domain_readiness' => ['operator.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $this->withToken($one['token'])->getJson('https://localhost/email/outbound-transports')->assertUnauthorized();
    $this->withToken('registry-secret')->getJson('https://localhost/email/outbound-transports')->assertOk()
        ->assertDontSee($secret)->assertJsonMissingPath('data.0.credential_ref');
    $this->withToken($one['token'])->postJson('https://localhost/email/outbound-transports/'.$first['id'].'/credential',
        ['password' => 'new-private-password'])->assertUnauthorized();
    $pool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'operator-pool', 'members' => [
            ['transport_id' => $first['id'], 'priority' => 0, 'weight' => 3],
            ['transport_id' => $second['id'], 'priority' => 1, 'weight' => 1],
        ],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'two', 'scope' => 'shared_route', 'sender_id' => $sender['id'], 'pool_id' => $pool['id'],
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'one', 'scope' => 'shared_route', 'sender_id' => $sender['id'], 'pool_id' => $pool['id'],
    ])->assertCreated();
    $plan = ['pool_id' => $pool['id'], 'account_id' => 'one', 'sender_domain' => 'operator.test',
        'delivery_id' => str_repeat('a', 32), 'route_id' => $sender['id'], 'domain_id' => ''];
    $this->withHeader('X-Gateway-Internal', 'wrong')->postJson('/_internal/gateway/email/outbound-plan', $plan)->assertNotFound();
    $planned = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/email/outbound-plan', $plan)->assertOk()->json();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->postJson('/_internal/gateway/extensions/webong/email/outbound-plan', $plan)->assertOk()
        ->assertJsonPath('transports.0.id', $first['id']);
    expect($planned['transports'][0]['id'])->toBe($first['id'])
        ->and($planned['transports'][0]['password'])->toBe($secret)
        ->and($planned['transports'][1]['id'])->toBe($second['id']);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/email/outbound-plan', $plan)
        ->assertOk()->assertJsonPath('transports.0.id', $first['id']);
    $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports/'.$first['id'].'/credential',
        ['password' => 'rotated-private-password'])->assertOk()->assertJsonMissingPath('password');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/email/outbound-plan', $plan)
        ->assertOk()->assertJsonPath('transports.0.password', 'rotated-private-password');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/email/outbound-plan',
        [...$plan, 'account_id' => 'two'])->assertConflict();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson('/_internal/gateway/email/outbound-attempts/'.$plan['delivery_id'], [
        'transport_id' => $first['id'], 'state' => 'attempted',
    ])->assertOk();
    $reserve = '/_internal/gateway/email/outbound-attempts/'.$plan['delivery_id'].'/reserve';
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($reserve,
        ['transport_id' => $first['id'], 'attempt_index' => 0])->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($reserve,
        ['transport_id' => $first['id'], 'attempt_index' => 0])->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')->postJson($reserve,
        ['transport_id' => $first['id'], 'attempt_index' => 1])->assertStatus(429);
    $this->withToken('registry-secret')->getJson('https://localhost/email/outbound-attempts')->assertOk()
        ->assertJsonPath('data.0.state', 'attempted')->assertDontSee($secret);
});

it('filters unhealthy and unauthorized transports and rejects a domain binding with ineligible members', function (): void {
    $store = app(OutboundTransportStore::class);
    $authorized = $store->createTransport(['name' => 'authorized', 'type' => 'smtp', 'address' => 'smtp.good.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true, 'authorized_domains' => ['sender.test']]);
    $authorized = $store->setDomainReadiness($authorized, 'sender.test', ['spf' => 'ready', 'dkim' => 'ready']);
    $wrong = $store->createTransport(['name' => 'wrong-domain', 'type' => 'smtp', 'address' => 'smtp.wrong.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true, 'authorized_domains' => ['other.test']]);
    $wrong = $store->setDomainReadiness($wrong, 'other.test', ['spf' => 'ready', 'dkim' => 'ready']);
    $pool = $store->createPool(['name' => 'mixed', 'members' => [
        ['transport_id' => $authorized->id, 'priority' => 0, 'weight' => 2],
        ['transport_id' => $wrong->id, 'priority' => 0, 'weight' => 1],
    ]]);
    $domain = app(PlannerResourceStore::class)->create('email.sending-domain', 'sender.test', [
        'account_id' => 'account', 'name' => 'sender.test', 'verified_at' => now('UTC')->toIso8601String(),
    ]);
    expect(fn () => $store->bind(['account_id' => 'account', 'scope' => 'domain',
        'sender_id' => $domain->id, 'pool_id' => $pool->id]))->toThrow(InvalidArgumentException::class);
    expect(array_map(static fn (object $row): string => $row->id,
        $store->ordered($pool->id, 'account', 'sender.test', str_repeat('b', 32))))->toBe([$authorized->id]);
    $store->setHealth($authorized, false);
    expect($store->ordered($pool->id, 'account', 'sender.test', str_repeat('b', 32)))->toBe([]);
});

it('authorizes a sender domain atomically and moves a binding only to an eligible pool', function (): void {
    config()->set('gateway.email.shared_from_address', 'support@operator.test');
    $account = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])
        ->assertCreated()->json();
    $sender = $this->withToken($account['token'])
        ->postJson('https://localhost/email/mailbox-accounts/one/sending-routes', ['mode' => 'shared'])
        ->assertCreated()->json();
    $current = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'current', 'type' => 'smtp', 'address' => 'smtp.current.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true,
        'authorized_domains' => ['operator.test'],
        'domain_readiness' => ['operator.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $currentPool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'current-pool',
        'members' => [['transport_id' => $current['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $binding = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-bindings', [
        'account_id' => 'one', 'scope' => 'shared_route', 'sender_id' => $sender['id'], 'pool_id' => $currentPool['id'],
    ])->assertCreated()->json();

    $replacement = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'replacement', 'type' => 'smtp', 'address' => 'smtp.replacement.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true,
        'authorized_domains' => ['another.test'],
        'domain_readiness' => ['another.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $authorized = $this->withToken('registry-secret')->patchJson('https://localhost/email/outbound-transports/'.$replacement['id'], [
        'domain_authorization' => [
            'domain' => 'operator.test',
            'status' => ['spf' => 'ready', 'dkim' => 'ready'],
        ],
    ])->assertOk()
        ->assertJsonPath('authorized_domains.1', 'operator.test')->json();
    expect($authorized['domain_readiness']['operator.test']['dkim'])->toBe('ready');
    $replacementPool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'replacement-pool',
        'members' => [['transport_id' => $replacement['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->patchJson('https://localhost/email/outbound-bindings/'.$binding['id'], [
        'pool_id' => $replacementPool['id'],
    ])->assertOk()->assertJsonPath('pool_id', $replacementPool['id']);

    $wrongAccount = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-transports', [
        'name' => 'wrong-account', 'type' => 'smtp', 'address' => 'smtp.wrong-account.test:587',
        'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p', 'healthy' => true,
        'account_id' => 'two', 'authorized_domains' => ['operator.test'],
        'domain_readiness' => ['operator.test' => ['spf' => 'ready', 'dkim' => 'ready']],
    ])->assertCreated()->json();
    $wrongPool = $this->withToken('registry-secret')->postJson('https://localhost/email/outbound-pools', [
        'name' => 'wrong-account-pool',
        'members' => [['transport_id' => $wrongAccount['id'], 'priority' => 0, 'weight' => 1]],
    ])->assertCreated()->json();
    $this->withToken('registry-secret')->patchJson('https://localhost/email/outbound-bindings/'.$binding['id'], [
        'pool_id' => $wrongPool['id'],
    ])->assertUnprocessable();
    $this->withToken('registry-secret')->getJson('https://localhost/email/outbound-bindings')->assertOk()
        ->assertJsonPath('data.0.pool_id', $replacementPool['id']);
});

it('selects weighted peers deterministically across store instances', function (): void {
    $store = app(OutboundTransportStore::class);
    $ids = [];
    foreach (['light', 'heavy'] as $name) {
        $row = $store->createTransport(['name' => $name, 'type' => 'smtp', 'address' => 'smtp.'.$name.'.test:587',
            'tls_mode' => 'starttls', 'username' => 'u', 'password' => 'p',
            'healthy' => true,
            'authorized_domains' => ['sender.test'],
            'domain_readiness' => ['sender.test' => ['spf' => 'ready', 'dkim' => 'ready']]]);
        $ids[$name] = $row->id;
    }
    $pool = $store->createPool(['name' => 'weighted', 'members' => [
        ['transport_id' => $ids['light'], 'priority' => 0, 'weight' => 1],
        ['transport_id' => $ids['heavy'], 'priority' => 0, 'weight' => 4],
    ]]);
    $counts = [$ids['light'] => 0, $ids['heavy'] => 0];
    for ($index = 0; $index < 100; $index++) {
        $id = str_pad(dechex($index), 32, '0', STR_PAD_LEFT);
        $selected = $store->ordered($pool->id, 'one', 'sender.test', $id)[0]->id;
        $counts[$selected]++;
        expect(app(OutboundTransportStore::class)->ordered($pool->id, 'one', 'sender.test', $id)[0]->id)->toBe($selected);
    }
    expect($counts[$ids['heavy']])->toBeGreaterThan($counts[$ids['light']]);
});
