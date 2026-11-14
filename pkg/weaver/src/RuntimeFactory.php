<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use PDO;
use Webong\WorkFlow\Services\{DefaultFlowStateSerializer,FlowExecutionDispatcher};

/** Shared connection/configuration for bridge and separately supervised worker. */
final readonly class RuntimeFactory
{
    public PDO $connection;
    public QueueScheduler $queue;
    private ApprovedOperations $executors;

    public function __construct(public RuntimeConfiguration $configuration,array $databaseSecret,array $privateContext=[])
    {
        $this->connection=ConnectionFactory::fromSecret($databaseSecret);
        OperationalSchema::assertReady($this->connection);
        $this->queue=new QueueScheduler($this->connection);
        $http=$configuration->httpDestinations===[]?null:new GatewayHttpDelivery($privateContext['delivery_url']??'',$privateContext['control_token']??'',$privateContext['http_bindings']??[]);
        foreach($configuration->httpRequirements as $requirement){
            $binding=$privateContext['http_bindings'][$requirement['destination']]??null;
            if(!is_array($binding) || ($binding['fingerprint']??null)!==$requirement['fingerprint'] || ($requirement['idempotent'] && ($binding['idempotent']??false)!==true)){
                throw new \InvalidArgumentException('HTTP destination fingerprint or replay guarantee does not match its published definition.');
            }
        }
        $this->executors=new ApprovedOperations($configuration->operations->keys(),$http);

    }

    public function application(string $account): AuthorizedRpcApplication
    {
        $definitions=$this->configuration->definitions[$account] ?? throw new RpcFailure(-32003,'Forbidden');
        $serializer=new DefaultFlowStateSerializer();
        $stores=match(OperationalSchema::driver($this->connection)) {
            'sqlite'=>new PdoSqliteFlowStateStoreFactory($this->connection,$serializer),
            'postgres'=>new PdoPostgresFlowStateStoreFactory($this->connection,$serializer),
            'mysql'=>new PdoMySqlFlowStateStoreFactory($this->connection,$serializer),
        };
        $app=new FlowRpcApplication($definitions,$stores,new FlowExecutionDispatcher([]),
            operationProviders:[$this->configuration->operations],grantedCapabilities:[],queue:new QueueRpcRuntime($this->queue));
        return new AuthorizedRpcApplication($app,$this->configuration->grants);
    }

    public function worker(): QueueWorker
    {
        return new QueueWorker($this->queue,[$this->executors],executionLeaseSeconds:$this->configuration->httpDestinations===[]?0:30);
    }
}
