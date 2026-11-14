<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;
use Webong\WorkFlow\Contracts\FlowStateSerializer;
use Webong\WorkFlow\Contracts\FlowStateStoreFactory;
use Webong\WorkFlow\Contracts\ForgettableFlowStateStore;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;

final readonly class PdoPostgresFlowStateStoreFactory implements FlowStateStoreFactory
{
    public function __construct(
        private PDO $connection,
        private FlowStateSerializer $serializer,
    ) {
    }

    public function for(FlowStateSubject $subject): ForgettableFlowStateStore
    {
        return new PdoPostgresFlowStateStore($this->connection, $this->serializer, $subject);
    }
}
