<?php

declare(strict_types=1);

it('stores a private DKIM key in the shared encrypted collection and exposes it only internally', function (): void {
    $private = openssl_pkey_new(['private_key_type' => OPENSSL_KEYTYPE_RSA, 'private_key_bits' => 2048]);
    expect($private)->not->toBeFalse();
    openssl_pkey_export($private, $pem);
    $this->withToken('registry-secret')->postJson('/email/dkim-keys', [
        'domain' => 'example.test', 'selector' => 'mail1', 'private_key_pem' => 'invalid',
    ])->assertUnprocessable();
    $created = $this->withToken('registry-secret')->postJson('/email/dkim-keys', [
        'domain' => 'example.test', 'selector' => 'mail1', 'private_key_pem' => $pem,
    ])->assertCreated();
    expect($created->json())->not->toHaveKey('private_key_pem');
    $this->withToken('registry-secret')->getJson('/email/dkim-keys/'.$created->json('id'))
        ->assertOk()->assertDontSee('PRIVATE KEY');
    $this->getJson('/_internal/gateway/email/dkim/example.test')->assertNotFound();
    $this->head('/_internal/gateway/email/dkim/example.test', ['X-Gateway-Internal' => 'internal-secret'])
        ->assertOk();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/email/dkim/example.test')->assertOk()
        ->assertJsonPath('selector', 'mail1')->assertJsonPath('private_key_pem', trim($pem));
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/webong/email/dkim/example.test')->assertOk()
        ->assertJsonPath('selector', 'mail1');
    $disabled = $this->withToken('registry-secret')->withHeaders(['If-Match' => $created->headers->get('ETag')])
        ->putJson('/email/dkim-keys/'.$created->json('id'), [
            'domain' => 'example.test', 'selector' => 'mail2', 'enabled' => false,
        ])->assertOk();
    expect($disabled->json('selector'))->toBe('mail2');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/email/dkim/example.test')->assertNotFound();
    $this->head('/_internal/gateway/email/dkim/example.test', ['X-Gateway-Internal' => 'internal-secret'])
        ->assertNotFound();
});
