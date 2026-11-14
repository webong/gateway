<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\App;

use PDO;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use Webong\Gateway\Weaver\PdoSqliteFlowStateStoreFactory;
use Webong\WorkFlow\Enums\FlowStatus;
use Webong\WorkFlow\Services\DefaultFlowStateSerializer;
use Webong\Gateway\Weaver\Tests\RunStoreAssertions;
use Webong\WorkFlow\ValueObjects\FlowState;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;

final class PdoSqliteFlowStateStoreTest extends TestCase
{
    use RunStoreAssertions;

    public function testItPersistsMutatesAndForgetsAPolymorphicState(): void
    {
        if (! in_array('sqlite', PDO::getAvailableDrivers(), true)) {
            self::fail('The pdo_sqlite extension is not available.');
        }

        $path = (string) tempnam(sys_get_temp_dir(), 'workflow-sqlite-');
        $connection = $this->connect($path);

        try {
            $schema = file_get_contents(dirname(__DIR__, 2).'/schema/sqlite.sql');
            if (! is_string($schema)) {
                throw new RuntimeException('Unable to read the workflow RPC schema');
            }
            $connection->exec($schema);

            $subject = new FlowStateSubject('test-order', bin2hex(random_bytes(8)));
            $store = (new PdoSqliteFlowStateStoreFactory($connection, new DefaultFlowStateSerializer()))->for($subject);

            self::assertNull($store->get('approval'));
            $state = $store->mutate('approval', static function (?FlowState $current): FlowState {
                self::assertNull($current);

                return new FlowState(FlowStatus::PENDING, metadata: ['execution_driver' => 'inline']);
            });
            self::assertSame($state->toArray(), $store->get('approval')?->toArray());

            // A separate connection must observe committed writes and be able
            // to continue the mutation cycle under the IMMEDIATE write lock.
            $secondConnection = $this->connect($path);
            $secondStore = (new PdoSqliteFlowStateStoreFactory($secondConnection, new DefaultFlowStateSerializer()))->for($subject);
            $updated = $secondStore->mutate('approval', static function (?FlowState $current): FlowState {
                self::assertNotNull($current);

                return $current->withMetadata(['completed' => true]);
            });
            self::assertEquals($updated->toArray(), $store->get('approval')?->toArray());

            $other = (new PdoSqliteFlowStateStoreFactory($connection, new DefaultFlowStateSerializer()))
                ->for(new FlowStateSubject('test-order', 'other-'.$subject->id));
            self::assertNull($other->get('approval'));

            $store->forget('approval');
            self::assertNull($secondStore->get('approval'));
            $this->assertRunIsolation($store);
            $this->assertStructuredOutputRoundTrip($store);
        } finally {
            unset($connection);
            @unlink($path);
            @unlink($path.'-wal');
            @unlink($path.'-shm');
        }
    }

    private function connect(string $path): PDO
    {
        $connection = new PDO('sqlite:'.$path, null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
        $connection->exec('PRAGMA busy_timeout = 5000');

        return $connection;
    }
}
