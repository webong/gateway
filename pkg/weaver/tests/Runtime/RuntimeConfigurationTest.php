<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\RuntimeConfiguration;

final class RuntimeConfigurationTest extends TestCase
{
    private function document(): array
    {
        return ['schema_version'=>1,'database_ref'=>'database','authentication_ref'=>'rpc-auth','operations'=>['gateway.approval'],
            'accounts'=>[['id'=>'a','definitions'=>[['key'=>'order','version'=>1,'steps'=>[['id'=>'approve','metadata'=>['operation'=>'gateway.approval']]]]],
                'grants'=>[['account'=>'a','flow_key'=>'order','subject'=>['type'=>'order','id'=>'1'],'actions'=>['flow.start']]]]]];
    }
    public function testConfigurationAcceptsOnlyApprovedDefinitionsAndSecretReferences(): void
    {
        $runtime=new RuntimeConfiguration($this->document());
        self::assertSame('database',$runtime->databaseReference);
        self::assertSame('rpc-auth',$runtime->authenticationReference);
        self::assertSame(1,$runtime->definitions['a']->definition('order')->version);
    }
    public function testCredentialsAndBootstrapPathsAreRejectedFromDesiredState(): void
    {
        foreach(['dsn','password','rpcToken','bootstrap','php','command'] as $field){
            try{new RuntimeConfiguration([...$this->document(),$field=>'untrusted']);self::fail('Unsupported setting accepted');}
            catch(\InvalidArgumentException $error){self::assertSame('Unsupported WorkFlow runtime configuration.',$error->getMessage());}
        }
    }
    public function testAccountCannotCarryAnotherAccountsGrant(): void
    {
        $document=$this->document();$document['accounts'][0]['grants'][0]['account']='b';
        $this->expectException(\InvalidArgumentException::class);
        new RuntimeConfiguration($document);
    }
    public function testMissingWeakOrUnconfiguredAccountCredentialsFailReadiness(): void
    {
        $configuration=new RuntimeConfiguration($this->document());
        foreach([[],['a'=>'short'],['b'=>str_repeat('x',32)]] as $credentials){
            try{$configuration->assertAuthentication($credentials);self::fail('Invalid authentication accepted');}
            catch(\InvalidArgumentException $error){self::assertNotEmpty($error->getMessage());}
        }
        $configuration->assertAuthentication(['a'=>str_repeat('x',32)]);
        self::assertCount(1,$configuration->definitions);
    }

}
