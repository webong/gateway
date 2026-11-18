<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Schema;
use Illuminate\Database\Schema\Blueprint;
use Webong\Proxy\Models\WebProxyCall;
use Webong\Proxy\Jobs\ProcessWebhookJob;

it('restores a real Proxy 0.3.1 processing payload with the adapter and original model identity', function (): void {
    Schema::create('webhook_calls', function (Blueprint $table): void {
        $table->id();
        $table->string('name');
        $table->text('url');
        $table->text('headers')->nullable();
        $table->text('payload')->nullable();
        $table->timestamps();
    });
    $call = WebProxyCall::create(['id' => 1, 'name' => 'retained', 'url' => 'https://operator.test/hook',
        'headers' => ['X-Existing' => 'retained'], 'payload' => ['message' => 'queued before upgrade']]);
    $fixture = json_decode(file_get_contents(__DIR__.'/../Fixtures/proxy-0.3.1/processing-job.json'), true, flags: JSON_THROW_ON_ERROR);
    $job = unserialize(base64_decode($fixture['payload_base64'], true));

    expect($job)->toBeInstanceOf(ProcessWebhookJob::class)
        ->and($job->queue)->toBe('legacy-webhooks')
        ->and($job->webhookCall)->toBeInstanceOf(WebProxyCall::class)
        ->and($job->webhookCall->getKey())->toBe($call->getKey())
        ->and($job->webhookCall->payload)->toBe(['message' => 'queued before upgrade'])
        ->and($job->webhookCall->headers)->toBe(['X-Existing' => 'retained']);
});
