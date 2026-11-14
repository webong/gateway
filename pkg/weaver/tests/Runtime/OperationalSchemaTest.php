<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\Runtime;

use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Webong\Gateway\Weaver\OperationalSchema;

final class OperationalSchemaTest extends TestCase
{
    public static function databases(): array { return [['sqlite'],['postgres'],['mysql']]; }

    #[DataProvider('databases')]
    public function testExplicitInstallIsIdempotentAndPreservesOperationalRows(string $driver): void
    {
        $pdo=$this->connection($driver);
        try {
            OperationalSchema::install($pdo,7);
            $pdo->exec("INSERT INTO workflow_queue_runs VALUES ('sentinel', '{}', '{}')");
            OperationalSchema::install($pdo,99);
            OperationalSchema::assertReady($pdo);
            self::assertSame(7,(int)$pdo->query('SELECT capacity FROM workflow_queue_lock WHERE id=1')->fetchColumn());
            self::assertSame('sentinel',$pdo->query('SELECT id FROM workflow_queue_runs')->fetchColumn());
        } finally { $this->drop($pdo); }
    }

    #[DataProvider('databases')]
    public function testExistingStandaloneDataIsNeverImplicitlyAdopted(string $driver): void
    {
        $pdo=$this->connection($driver);
        try {
            $pdo->exec('CREATE TABLE workflow_rpc_states (sentinel INTEGER)');
            $pdo->exec('INSERT INTO workflow_rpc_states VALUES (42)');
            try {OperationalSchema::install($pdo);self::fail('Legacy data adopted');}
            catch(\RuntimeException $error){self::assertStringContainsString('explicit migration rehearsal',$error->getMessage());}
            self::assertSame(42,(int)$pdo->query('SELECT sentinel FROM workflow_rpc_states')->fetchColumn());
        } finally {$this->drop($pdo);}
    }

    #[DataProvider('databases')]
    public function testFutureVersionOrMissingTablesFailReadiness(string $driver): void
    {
        $pdo=$this->connection($driver);
        try {
            OperationalSchema::install($pdo);
            $pdo->exec('UPDATE gateway_workflow_schema SET version=99');
            try {OperationalSchema::assertReady($pdo);self::fail('Future schema accepted');}
            catch(\RuntimeException $error){self::assertStringContainsString('not ready',$error->getMessage());}
            $pdo->exec('UPDATE gateway_workflow_schema SET version=1');
            $pdo->exec('DROP TABLE workflow_queue_claims');
            $this->expectException(\RuntimeException::class);
            OperationalSchema::assertReady($pdo);
        } finally {$this->drop($pdo);}
    }

    private function connection(string $driver): PDO
    {
        if($driver==='sqlite'){$pdo=new PDO('sqlite::memory:');}
        else {
            $key=$driver==='postgres'?'POSTGRES':'MYSQL';
            $dsn=getenv('WORK_FLOW_'.$key.'_DSN');
            self::assertIsString($dsn,'SQL integration service is required');
            self::assertMatchesRegularExpression('/dbname=workflow_test(?:;|$)/',$dsn,'Use the disposable workflow_test database');
            $pdo=new PDO($dsn,getenv('WORK_FLOW_'.$key.'_USER'),getenv('WORK_FLOW_'.$key.'_PASSWORD'));
        }
        $pdo->setAttribute(PDO::ATTR_ERRMODE,PDO::ERRMODE_EXCEPTION);
        $this->drop($pdo);return $pdo;
    }
    private function drop(PDO $pdo): void
    {
        foreach(['gateway_workflow_schema','workflow_queue_outbox','workflow_queue_claims','workflow_queue_runs','workflow_queue_lock','workflow_rpc_states'] as $table){$pdo->exec('DROP TABLE IF EXISTS '.$table);}
    }
}
