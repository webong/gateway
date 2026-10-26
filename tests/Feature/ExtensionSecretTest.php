<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;

it('keeps provider secrets out of revisioned Extension configuration and resolves only the owning identity', function (): void {
    $email = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'webong/email', 'enabled' => true,
    ])->assertCreated()->json();
    $dns = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'webong/dns', 'enabled' => true,
    ])->assertCreated()->json();

    $created = $this->withToken('registry-secret')->putJson(
        'https://localhost/extensions/'.$email['id'].'/secrets/primary-relay',
        ['value' => ['username' => 'relay-user', 'password' => 'relay-secret']],
    )->assertCreated()
        ->assertJsonPath('reference', 'primary-relay')
        ->assertJsonPath('version', 1)
        ->assertJsonMissingPath('value');

    $ciphertext = DB::table('configuration_secrets')->where('resource_id', $email['id'])->value('ciphertext');
    expect($ciphertext)->toBeString()->not->toContain('relay-user', 'relay-secret')
        ->and(Crypt::decryptString($ciphertext))->toContain('relay-secret');

    $this->withToken('registry-secret')->getJson('https://localhost/extensions/'.$email['id'].'/secrets')
        ->assertOk()->assertExactJson(['data' => [['reference' => 'primary-relay', 'version' => 1]]]);

    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/webong/email/secrets/primary-relay')
        ->assertOk()
        ->assertJsonPath('value.username', 'relay-user')
        ->assertJsonPath('value.password', 'relay-secret')
        ->assertHeader('Cache-Control', 'no-store, private');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/webong/dns/secrets/primary-relay')
        ->assertNotFound();

    $desired = $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions')->assertOk()->json();
    expect(json_encode($desired))->not->toContain('relay-user', 'relay-secret', 'primary-relay');

    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')->putJson(
        'https://localhost/extensions/'.$email['id'].'/secrets/primary-relay',
        ['value' => ['username' => 'relay-user', 'password' => 'rotated-secret']],
    )->assertOk()->assertJsonPath('version', 2)->assertJsonMissingPath('value');
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')->putJson(
        'https://localhost/extensions/'.$email['id'].'/secrets/primary-relay',
        ['value' => ['username' => 'relay-user', 'password' => 'stale-secret']],
    )->assertStatus(412);

    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')->putJson(
        'https://localhost/extensions/'.$dns['id'].'/secrets/primary-relay',
        ['value' => ['token' => 'not-created']],
    )->assertStatus(412);

    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')->putJson(
        '/extensions/'.$email['id'],
        ['extension' => 'webong/email', 'enabled' => false],
    )->assertOk()->assertJsonPath('enabled', false);
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/webong/email/secrets/primary-relay')
        ->assertNotFound();
});

it('requires HTTPS and never returns a write-only Extension secret through the public CMS', function (): void {
    $extension = $this->withToken('registry-secret')->postJson('/extensions', [
        'extension' => 'example/provider', 'enabled' => true,
    ])->assertCreated()->json();

    $this->withToken('registry-secret')->putJson(
        '/extensions/'.$extension['id'].'/secrets/provider-token',
        ['value' => ['token' => 'secret']],
    )->assertForbidden();
    $this->withToken('registry-secret')->putJson(
        'https://localhost/extensions/'.$extension['id'].'/secrets/Invalid',
        ['value' => ['token' => 'secret']],
    )->assertUnprocessable();
    $this->withToken('registry-secret')->putJson(
        'https://localhost/extensions/'.$extension['id'].'/secrets/provider-token',
        ['value' => ['token' => 'secret']],
    )->assertCreated()->assertJsonMissingPath('value');
    $this->withToken('registry-secret')->getJson('https://localhost/extensions/'.$extension['id'])
        ->assertOk()->assertDontSee('secret');

    $this->withToken('registry-secret')->deleteJson(
        'https://localhost/extensions/'.$extension['id'].'/secrets/provider-token',
    )->assertStatus(428);
    $this->withToken('registry-secret')->withHeader('If-Match', '"1"')->deleteJson(
        'https://localhost/extensions/'.$extension['id'].'/secrets/provider-token',
    )->assertNoContent();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->getJson('/_internal/gateway/extensions/example/provider/secrets/provider-token')
        ->assertNotFound();
});
