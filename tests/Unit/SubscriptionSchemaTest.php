<?php

declare(strict_types=1);

use Webong\WebRelay\Protocol\MatchRules;
use Webong\WebRelay\Protocol\MatchRulesPatch;
use Webong\WebRelay\Protocol\SubscriptionState;

it('keeps the subscription JSON schema aligned with match-rules v1', function (): void {
    $schema = json_decode(
        file_get_contents(dirname(__DIR__, 2).'/docs/schemas/subscription-v1.schema.json'),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    $matchRules = new ReflectionClass(MatchRules::class);

    expect($schema['$defs']['match']['properties']['version']['const'])
        ->toBe(MatchRules::VERSION)
        ->and($schema['$defs']['match']['properties']['rules']['maxProperties'])
        ->toBe($matchRules->getConstant('MAX_FIELDS'))
        ->and($schema['$defs']['match']['properties']['rules']['additionalProperties']['oneOf'][0]['maxItems'])
        ->toBe($matchRules->getConstant('MAX_RULES_PER_FIELD'))
        ->and($schema['$defs']['rule']['maxLength'])
        ->toBe($matchRules->getConstant('MAX_RULE_LENGTH'))
        ->and($schema['$defs']['ruleName']['enum'])
        ->toBe(array_keys($matchRules->getConstant('ALLOWED_RULES')));
});

it('keeps the incremental match-patch schema aligned with its PHP contract', function (): void {
    $schema = json_decode(
        file_get_contents(dirname(__DIR__, 2).'/docs/schemas/subscription-match-patch-v1.schema.json'),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    $patch = new ReflectionClass(MatchRulesPatch::class);

    expect($schema['properties']['version']['const'])
        ->toBe(MatchRulesPatch::VERSION)
        ->and($schema['properties']['operations']['maxItems'])
        ->toBe($patch->getConstant('MAX_OPERATIONS'));
});

it('keeps lifecycle schemas aligned with subscription states', function (): void {
    $status = json_decode(
        file_get_contents(dirname(__DIR__, 2).'/docs/schemas/subscription-status-v1.schema.json'),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );
    $update = json_decode(
        file_get_contents(dirname(__DIR__, 2).'/docs/schemas/subscription-update-v1.schema.json'),
        true,
        512,
        JSON_THROW_ON_ERROR,
    );

    expect($status['properties']['status']['enum'])
        ->toBe([SubscriptionState::ACTIVE, SubscriptionState::PAUSED])
        ->and($update['properties']['type']['enum'])
        ->toBe(['relay', 'reply']);
});
