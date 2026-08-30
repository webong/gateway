<?php

declare(strict_types=1);

use Webong\Gateway\Tests\TestCase;

uses(TestCase::class)->in('Feature', 'Unit');
uses(TestCase::class)->in('Benchmark');

foreach (glob(dirname(__DIR__).'/ext/*/tests/Pest.php') ?: [] as $extensionPest) {
    require $extensionPest;
}
