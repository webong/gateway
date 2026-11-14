<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\{ApprovedOperations,PublishedDefinitions};

final class PublishedDefinitionsTest extends TestCase
{
    private const DOCUMENT=['key'=>'order','version'=>1,'steps'=>[
        ['id'=>'approve','metadata'=>['operation'=>'gateway.approval']],
        ['id'=>'fulfil','depends_on'=>['approve'],'metadata'=>['operation'=>'gateway.complete']],
    ]];
    public function testPublishedDefinitionSelectsOnlyDeployedApprovedOperations(): void
    {
        $definitions=new PublishedDefinitions([self::DOCUMENT],new ApprovedOperations(ApprovedOperations::KEYS));
        self::assertSame(1,$definitions->definition('order')->version);
        self::assertCount(2,$definitions->definition('order')->steps);
    }
    public function testUnapprovedOperationFailsBeforeRuntimeStarts(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new PublishedDefinitions([self::DOCUMENT],new ApprovedOperations(['gateway.approval']));
    }
    public function testClientSelectedClassOrCommandIsNeverAnOperation(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new PublishedDefinitions([[...self::DOCUMENT,'steps'=>[['id'=>'x','metadata'=>['operation'=>'php:system']]]]],new ApprovedOperations(ApprovedOperations::KEYS));
    }
    public function testAmbiguousVersionsFailActivation(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new PublishedDefinitions([self::DOCUMENT,[...self::DOCUMENT,'version'=>2]],new ApprovedOperations(ApprovedOperations::KEYS));
    }
}
