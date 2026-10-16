<?php

declare(strict_types=1);

use Webong\Gateway\ShellHookGrantStore;

it('manages exact Shell hook grants through Planner with separate mutation authority', function (): void {
    config()->set('gateway.shell.subscription_token', 'shell-admin-secret');
    $input = ['endpoint_key' => 'relay-endpoint', 'subscriber_id' => 'shell-owner',
        'hook' => 'github/deploy'];
    $this->postJson('/shell/hook-grants', $input)->assertUnauthorized();
    $this->withToken('registry-secret')->postJson('/shell/hook-grants', $input)->assertForbidden();
    $created = $this->withToken('registry-secret')
        ->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret')
        ->postJson('/shell/hook-grants', $input)
        ->assertCreated()->assertHeader('ETag', '"1"')->json();
    $this->withToken('registry-secret')->getJson('/shell/hook-grants')
        ->assertOk()->assertJsonCount(1, 'data');
    expect(app(ShellHookGrantStore::class)->allowed('relay-endpoint', 'shell-owner', 'github/deploy'))->toBeTrue()
        ->and(app(ShellHookGrantStore::class)->allowed('other-endpoint', 'shell-owner', 'github/deploy'))->toBeFalse();

    $this->withToken('registry-secret')->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret')
        ->withHeader('If-Match', '"1"')
        ->putJson('/shell/hook-grants/'.$created['id'], [...$input, 'enabled' => false])
        ->assertOk()->assertHeader('ETag', '"2"');
    expect(app(ShellHookGrantStore::class)->allowed('relay-endpoint', 'shell-owner', 'github/deploy'))->toBeFalse();
    $this->withToken('registry-secret')->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret')
        ->withHeader('If-Match', '"1"')->deleteJson('/shell/hook-grants/'.$created['id'])
        ->assertStatus(412);
    $this->withToken('registry-secret')->withHeader('X-Gateway-Shell-Subscription', 'shell-admin-secret')
        ->withHeader('If-Match', '"2"')->deleteJson('/shell/hook-grants/'.$created['id'])
        ->assertNoContent();
    $this->withToken('registry-secret')
        ->getJson('/configurations/shell.hook-grant/'.$created['id'].'/revisions')
        ->assertOk()->assertJsonCount(3, 'data');
});
