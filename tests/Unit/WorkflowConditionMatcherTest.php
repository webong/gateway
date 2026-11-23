<?php

declare(strict_types=1);

use Webong\Gateway\Laravel\RequestMatcher;
use Webong\Gateway\Laravel\SubscriptionMatcher;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\MatchRules;
use Webong\Gateway\Protocols\MatchRulesPatch;
use Webong\WorkFlow\Conditions\ConditionAuthoringValidator;
use Webong\WorkFlow\Conditions\GuardedStepExecutor;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Enums\FlowStepStatus;
use Webong\WorkFlow\Services\FlowRunner;
use Webong\WorkFlow\ValueObjects\{ArrayFlowContext, FlowDefinition, FlowRun, FlowStepDefinition, FlowStepInvocation, StepResult};

function conditionRequest(string $protocol = 'http'): IngressRequest
{
    return new IngressRequest(deliveryId: 'condition', method: 'POST', host: 'relay.example.test',
        path: '/events', rawQuery: 'source=meta', headers: ['X-Tenant' => ['tenant-a']],
        body: '{"kind":"created","score":7}', protocol: $protocol);
}

it('evaluates jq directly against the normalized Gateway matching document', function (): void {
    $match = MatchRules::fromArray(['version' => 'v1', 'syntax' => 'jq',
        'expression' => '.headers["x-tenant"] == "tenant-a" and .payload.kind == "created" and .query.source == "meta"']);
    $matcher = app(RequestMatcher::class);
    $matcher->validate($match);
    expect($matcher->matches(conditionRequest(), $match))->toBeTrue()
        ->and($matcher->matches(conditionRequest(), MatchRules::fromArray([
            'version' => 'v1', 'syntax' => 'jq', 'expression' => '.input.payload.kind == "created"',
        ])))->toBeFalse();
});

it('normalizes and protects every rules leaf inside compositions', function (): void {
    $match = MatchRules::fromArray(['version' => 'v1', 'all' => [
        ['rules' => ['headers.X-Tenant' => 'required|in:tenant-a']],
        ['any' => [
            ['syntax' => 'jq', 'expression' => '.payload.kind == "deleted"'],
            ['not' => ['syntax' => 'jq', 'expression' => '.payload.score < 5']],
        ]],
    ]]);
    expect($match->toArray()['all'][0]['rules'])->toHaveKey('headers.x-tenant')
        ->and(app(RequestMatcher::class)->matches(conditionRequest(), $match))->toBeTrue();
    expect(fn () => MatchRules::fromArray(['version' => 'v1', 'not' => ['rules' => ['application.secret' => 'required']]]))
        ->toThrow(InvalidArgumentException::class);
    expect(fn () => MatchRulesPatch::make()->add('payload.kind', 'required')->apply($match))
        ->toThrow(InvalidArgumentException::class, 'Replace jq/composite');
    $httpOnly = MatchRules::fromArray(['version' => 'v1', 'not' => ['rules' => ['method' => 'required']]]);
    expect(app(RequestMatcher::class)->matches(conditionRequest('dns'), $httpOnly))->toBeFalse();
});

it('fails closed for corrupt stored conditions and nonboolean jq results', function (mixed $stored): void {
    expect(app(SubscriptionMatcher::class)->matches(conditionRequest(), [MatchRules::METADATA_KEY => $stored]))->toBeFalse();
})->with([
    'explicit null' => [null],
    'unknown driver' => [['version' => 'v1', 'syntax' => 'shell', 'expression' => 'true']],
    'invalid expression' => [['version' => 'v1', 'syntax' => 'jq', 'expression' => '.payload[']],
    'nonboolean result' => [['version' => 'v1', 'syntax' => 'jq', 'expression' => '.payload.score']],
    'hidden unsafe rule' => [['version' => 'v1', 'all' => [['rules' => ['payload.id' => 'exists:private_users,id']]]]],
]);

it('uses the configured drivers for both authoring and guarded execution without auto-enabling an engine', function (): void {
    $condition = ['syntax' => 'jq', 'expression' => '.context.enabled == true'];
    expect(app(ConditionAuthoringValidator::class)->validate($condition))->toBe([]);
    $effects = new class implements FlowStepExecutor {
        public int $calls = 0;
        public function supports(FlowStepDefinition $step): bool { return true; }
        public function execute(FlowStepInvocation $invocation): StepResult { $this->calls++; return StepResult::completed(); }
    };
    $definition = new FlowDefinition('gateway-guard', [new FlowStepDefinition('effect', 'Effect', metadata: ['when' => $condition])]);
    $run = new FlowRun('gateway-guard-run', $definition);
    $guard = app()->make(GuardedStepExecutor::class, ['inner' => $effects]);
    $state = (new FlowRunner())->run($definition, $run->initialState(), new ArrayFlowContext(['enabled' => false]), [$guard]);
    expect($state->steps['effect']->status)->toBe(FlowStepStatus::SKIPPED)->and($effects->calls)->toBe(0);
});
