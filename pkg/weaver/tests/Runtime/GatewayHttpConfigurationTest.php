<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\{GatewayHttpDelivery,OperationalSchema,RuntimeConfiguration,RuntimeFactory};

final class GatewayHttpConfigurationTest extends TestCase
{
    private const PIN='124e5a7238e4e87786f8b225e0d4f34c7ffe0ef8377bea00ac262c3885e070fb';
    private function document(): array
    {
        return ['schema_version'=>1,'database_ref'=>'database','authentication_ref'=>'authentication','operations'=>['gateway.http.v1'],
            'http_destinations'=>['orders.v1'=>'orders-secret'],'accounts'=>[['id'=>'account','definitions'=>[['key'=>'order','version'=>1,'steps'=>[
                ['id'=>'send','retriable'=>true,'retry_policy'=>['enabled'=>true,'max_attempts'=>2,'idempotent'=>true],
                    'metadata'=>['operation'=>'gateway.http.v1','destination'=>'orders.v1','destination_sha256'=>self::PIN]],
            ]]],'grants'=>[]]]];
    }

    public function testProviderFingerprintHasStableCrossRuntimeRepresentation(): void
    {
        self::assertSame(self::PIN,GatewayHttpDelivery::fingerprint('https://provider.example/fulfil','provider-account-1'));
    }

    public function testHttpDefinitionsRequireSelectedScopedSecretAliases(): void
    {
        $document=$this->document();$document['http_destinations']=[];
        $this->expectException(\InvalidArgumentException::class);new RuntimeConfiguration($document);
    }

    public function testClientUrlsCannotReplaceScopedSecretReferences(): void
    {
        $document=$this->document();$document['http_destinations']=['orders.v1'=>'https://client.example/fulfil'];
        $this->expectException(\InvalidArgumentException::class);new RuntimeConfiguration($document);
    }

    public function testReadinessRejectsChangedDestinationAndMissingProviderReplayGuarantee(): void
    {
        $path=tempnam(sys_get_temp_dir(),'weaver-http-');$pdo=new PDO('sqlite:'.$path);OperationalSchema::install($pdo);
        try {
            foreach([['fingerprint'=>str_repeat('f',64),'idempotent'=>true],['fingerprint'=>self::PIN,'idempotent'=>false]] as $binding){
                try {
                    new RuntimeFactory(new RuntimeConfiguration($this->document()),['dsn'=>'sqlite:'.$path],
                        ['delivery_url'=>'http://127.0.0.1:1234/internal/deliver','control_token'=>str_repeat('x',32),'http_bindings'=>['orders.v1'=>$binding]]);
                    self::fail('Unsafe HTTP effect reached readiness');
                }catch(\InvalidArgumentException $error){self::assertStringContainsString('fingerprint or replay guarantee',$error->getMessage());}
            }
            self::assertSame(0,(int)$pdo->query('SELECT COUNT(*) FROM workflow_queue_claims')->fetchColumn());
        }finally{unset($pdo);foreach([$path,$path.'-wal',$path.'-shm'] as $file){if(is_file($file)){unlink($file);}}}
    }
}
