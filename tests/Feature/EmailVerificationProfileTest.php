<?php

declare(strict_types=1);

use Webong\Gateway\Email\TxtResolver;

it('keeps existing records and lets the operator brand new domains', function (): void {
    $dns = new class implements TxtResolver {
        public array $records = [];
        public function records(string $name): array { return $this->records[$name] ?? []; }
    };
    app()->instance(TxtResolver::class, $dns);
    $one = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'one'])
        ->assertCreated()->json();
    $two = $this->withToken('registry-secret')->postJson('https://localhost/email/mailbox-accounts', ['id' => 'two'])
        ->assertCreated()->json();
    $base = 'https://localhost/email/mailbox-accounts/one';
    $legacy = $this->withToken($one['token'])->postJson($base.'/receiving-domains', ['domain' => 'legacy.test'])
        ->assertCreated()->assertJsonPath('records.txt.name', '_gateway-verify.legacy.test')->json();
    $dns->records[$legacy['records']['txt']['name']] = [$legacy['records']['txt']['value']];
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$legacy['id'].'/verify')->assertOk();
    $this->withToken($one['token'])->postJson('https://localhost/email/verification-profile', [
        'label' => '_zorvia-verify', 'value_prefix' => 'zorvia-verification=',
    ])->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('https://localhost/email/verification-profile', [
        'label' => '_zorvia-verify', 'value_prefix' => 'zorvia-verification=',
    ])->assertCreated()->assertHeader('ETag', '"1"');
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('https://localhost/email/verification-profile', [
            'label' => 'bad.name', 'value_prefix' => 'unsafe value',
        ])->assertUnprocessable();
    $this->withToken($one['token'])->getJson($base.'/receiving-domains/'.$legacy['id'])
        ->assertJsonPath('records.txt.name', '_gateway-verify.legacy.test');

    $receiving = $this->withToken($one['token'])->postJson($base.'/receiving-domains', ['domain' => 'new.test'])
        ->assertCreated()->assertJsonPath('records.txt.name', '_zorvia-verify.new.test')->json();
    $sending = $this->withToken($one['token'])->postJson($base.'/sending-domains', ['domain' => 'new.test'])
        ->assertCreated()->assertJsonPath('records.txt.value', $receiving['records']['txt']['value'])->json();
    $dns->records[$receiving['records']['txt']['name']] = [$receiving['records']['txt']['value']];
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$receiving['id'].'/verify')->assertOk();
    $this->withToken($one['token'])->postJson($base.'/sending-domains/'.$sending['id'].'/verify')->assertOk();
    $this->withToken($two['token'])->getJson($base.'/receiving-domains/'.$receiving['id'].'/migration-record')
        ->assertUnauthorized();

    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('https://localhost/email/verification-profile', [
            'label' => '_builder-proof', 'value_prefix' => 'builder-proof=',
        ])->assertOk();
    $this->withToken($one['token'])->getJson($base.'/sending-domains/'.$sending['id'])
        ->assertJsonPath('records.txt.name', '_zorvia-verify.new.test');
    $this->withToken($one['token'])->getJson($base.'/receiving-domains/'.$legacy['id'].'/migration-record')
        ->assertJsonPath('txt.name', '_builder-proof.legacy.test');
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$legacy['id'].'/migrate')
        ->assertConflict();
    $this->withToken($one['token'])->getJson($base.'/receiving-domains/'.$legacy['id'])
        ->assertJsonPath('records.txt.name', '_gateway-verify.legacy.test');

    $token = substr($legacy['records']['txt']['value'], strlen('gateway-verification='));
    $dns->records['_builder-proof.legacy.test'] = ['builder-proof='.$token];
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$legacy['id'].'/migrate')
        ->assertOk()->assertJsonPath('records.txt.name', '_builder-proof.legacy.test');
    $this->withToken($one['token'])->postJson($base.'/receiving-domains/'.$legacy['id'].'/verify')
        ->assertOk();

    $newToken = substr($receiving['records']['txt']['value'], strlen('zorvia-verification='));
    $dns->records['_builder-proof.new.test'] = ['builder-proof='.$newToken];
    $this->withToken($one['token'])->postJson($base.'/sending-domains/'.$sending['id'].'/migrate')
        ->assertOk()->assertJsonPath('records.txt.name', '_builder-proof.new.test');
    $this->withToken($one['token'])->getJson($base.'/receiving-domains/'.$receiving['id'])
        ->assertJsonPath('records.txt.name', '_builder-proof.new.test');
});

it('brands operator forwarding domains and blocks migration before ownership is verified', function (): void {
    $dns = new class implements TxtResolver {
        public array $records = [];
        public function records(string $name): array { return $this->records[$name] ?? []; }
    };
    app()->instance(TxtResolver::class, $dns);
    $this->withToken('registry-secret')->postJson('https://localhost/email/verification-profile', [
        'value_prefix' => 'builder-proof=', 'label' => '_builder-proof',
    ])->assertCreated();
    $domain = $this->withToken('registry-secret')->postJson('https://localhost/email/domains', [
        'domain' => 'forward.test',
    ])->assertCreated()->assertJsonPath('verification.name', '_builder-proof.forward.test')->json();
    $this->withToken('registry-secret')->postJson('https://localhost/email/domains/'.$domain['id'].'/migrate')
        ->assertConflict();
    $dns->records[$domain['verification']['name']] = [$domain['verification']['value']];
    $this->withToken('registry-secret')->postJson('https://localhost/email/domains/'.$domain['id'].'/verify')
        ->assertOk();
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')
        ->putJson('https://localhost/email/verification-profile', [
            'label' => '_other-proof', 'value_prefix' => 'other-proof=',
        ])->assertOk();
    $this->withToken('registry-secret')->getJson('https://localhost/email/domains/'.$domain['id'])
        ->assertJsonPath('verification.name', '_builder-proof.forward.test');
    $this->withToken('registry-secret')->getJson('https://localhost/email/domains/'.$domain['id'].'/migration-record')
        ->assertJsonPath('txt.name', '_other-proof.forward.test');
    $this->withToken('registry-secret')->postJson('https://localhost/email/domains/'.$domain['id'].'/migrate')
        ->assertConflict();
    $token = substr($domain['verification']['value'], strlen('builder-proof='));
    $dns->records['_other-proof.forward.test'] = ['other-proof='.$token];
    $this->withToken('registry-secret')->postJson('https://localhost/email/domains/'.$domain['id'].'/migrate')
        ->assertOk()->assertJsonPath('verification.name', '_other-proof.forward.test');
});
