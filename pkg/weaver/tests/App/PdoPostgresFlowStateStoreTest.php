<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\App;

use PDO;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\Gateway\Weaver\PdoPostgresFlowStateStoreFactory;
use Webong\WorkFlow\Services\DefaultFlowStateSerializer;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;

final class PdoPostgresFlowStateStoreTest extends TestCase
{
    use \Webong\Gateway\Weaver\Tests\RunStoreAssertions;
    public function testItPersistsMutatesAndForgetsAPolymorphicState(): void
    {
        $dsn = getenv('WORK_FLOW_POSTGRES_DSN');
        if (! is_string($dsn) || $dsn === '') {
            self::fail('PostgreSQL integration environment is not configured.');
        }

        $connection = new PDO(
            $dsn,
            getenv('WORK_FLOW_POSTGRES_USER') ?: null,
            getenv('WORK_FLOW_POSTGRES_PASSWORD') ?: null,
            [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION],
        );
        $schema = file_get_contents(dirname(__DIR__, 2).'/schema/postgres.sql');
        if (! is_string($schema)) {
            throw new RuntimeException('Unable to read the workflow RPC schema');
        }
        $connection->exec($schema);

        $subject = new FlowStateSubject('test-order', bin2hex(random_bytes(8)));
        $store = (new PdoPostgresFlowStateStoreFactory($connection, new DefaultFlowStateSerializer()))->for($subject);

        self::assertNull($store->get('approval'));
        $state = $store->mutate('approval', static function (?FlowState $current): FlowState {
            self::assertNull($current);

            return new FlowState(FlowStatus::PENDING, metadata: ['execution_driver' => 'inline']);
        });
        self::assertSame($state->toArray(), $store->get('approval')?->toArray());

        $secondConnection = new PDO(
            $dsn,
            getenv('WORK_FLOW_POSTGRES_USER') ?: null,
            getenv('WORK_FLOW_POSTGRES_PASSWORD') ?: null,
            [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION],
        );
        $secondStore = (new PdoPostgresFlowStateStoreFactory($secondConnection, new DefaultFlowStateSerializer()))->for($subject);
        $updated = $secondStore->mutate('approval', static function (?FlowState $current): FlowState {
            self::assertNotNull($current);

            return $current->withMetadata(['completed' => true]);
        });
        self::assertEquals($updated->toArray(), $store->get('approval')?->toArray());

        $other = (new PdoPostgresFlowStateStoreFactory($connection, new DefaultFlowStateSerializer()))
            ->for(new FlowStateSubject('test-order', 'other-'.$subject->id));
        self::assertNull($other->get('approval'));

        $store->forget('approval');
        self::assertNull($secondStore->get('approval'));
        $this->assertRunIsolation($store);
        $this->assertStructuredOutputRoundTrip($store);
    }
}
