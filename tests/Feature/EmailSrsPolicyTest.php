<?php

declare(strict_types=1);

use Illuminate\Support\Facades\DB;

it('keeps SRS keys private and supports conditional rotation', function (): void {
    $old = base64_encode(str_repeat('o', 32));
    $new = base64_encode(str_repeat('n', 32));
    $created = $this->withToken('registry-secret')->postJson('/email/srs-policy', [
        'domain' => 'returns.example.test', 'keys' => [$old],
    ])->assertCreated();
    $id = $created->json('id');
    expect($created->json())->not->toHaveKey('keys');
    expect($created->json('key_count'))->toBe(1);
    $this->withToken('registry-secret')->getJson('/email/srs-policy')->assertOk()
        ->assertJsonMissing(['keys' => [$old]]);
    $this->withToken('registry-secret')->putJson('/email/srs-policy', [
        'keys' => [$new, $old],
    ])->assertStatus(428);
    $rotated = $this->withToken('registry-secret')->withHeaders(['If-Match' => $created->headers->get('ETag')])
        ->putJson('/email/srs-policy', ['keys' => [$new, $old]])->assertOk();
    expect($rotated->json('key_count'))->toBe(2);
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $created->headers->get('ETag')])
        ->putJson('/email/srs-policy', ['rewrite_enabled' => false])->assertStatus(412);
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $rotated->headers->get('ETag')])
        ->deleteJson('/email/srs-policy')->assertNoContent();
    expect(DB::table('configuration_revisions')->where('resource_id', $id)->count())->toBe(3);
});

it('retains SRS verification material while return tokens remain live', function (): void {
    $old = base64_encode(str_repeat('o', 32));
    $new = base64_encode(str_repeat('n', 32));
    $created = $this->withToken('registry-secret')->postJson('/email/srs-policy', [
        'domain' => 'returns.example.test', 'keys' => [$old],
    ])->assertCreated();
    DB::table('srs_returns')->insert(['token_hash' => str_repeat('a', 64),
        'original_sender' => 'encrypted', 'expires_at' => now()->addDay(),
        'created_at' => now(), 'updated_at' => now()]);
    $etag = $created->headers->get('ETag');
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $etag])
        ->putJson('/email/srs-policy', ['keys' => [$new]])->assertUnprocessable();
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $etag])
        ->deleteJson('/email/srs-policy')->assertUnprocessable();
    $this->withToken('registry-secret')->withHeaders(['If-Match' => $etag])
        ->putJson('/email/srs-policy', ['enabled' => false])->assertOk();
});
