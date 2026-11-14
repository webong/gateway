<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

/** Validated projection of Planner-approved desired state, with secret references only. */
final readonly class RuntimeConfiguration
{
    public string $databaseReference;
    public string $authenticationReference;
    public int $leaseSeconds;
    public string $workerMode;
    public array $httpDestinations;
    public array $httpRequirements;
    public ApprovedOperations $operations;
    /** @var array<string, PublishedDefinitions> */
    public array $definitions;
    /** @var list<RunGrant> */
    public array $grants;

    public function __construct(array $document)
    {
        if (array_diff(array_keys($document),['schema_version','database_ref','authentication_ref','lease_seconds','operations','accounts','worker_mode','http_destinations']) !== []
            || ($document['schema_version'] ?? null)!==1) {
            throw new \InvalidArgumentException('Unsupported WorkFlow runtime configuration.');
        }
        foreach (['database_ref','authentication_ref'] as $key) {
            if (!is_string($document[$key] ?? null) || preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D',$document[$key])!==1) {
                throw new \InvalidArgumentException('WorkFlow requires identity-scoped secret references.');
            }
        }
        $this->databaseReference=$document['database_ref'];
        $this->authenticationReference=$document['authentication_ref'];
        $lease=$document['lease_seconds'] ?? 300;
        if (!is_int($lease) || $lease<1 || $lease>3600) {throw new \InvalidArgumentException('Invalid worker lease duration.');}
        $this->leaseSeconds=$lease;
        $mode=$document['worker_mode']??'gateway';
        if(!in_array($mode,['gateway','poll'],true)){throw new \InvalidArgumentException('Invalid worker delivery mode.');}
        $this->workerMode=$mode;
        $destinations=$document['http_destinations']??[];
        if(!is_array($destinations) || count($destinations)>16){throw new \InvalidArgumentException('Invalid HTTP destination references.');}
        foreach($destinations as $name=>$reference){
            if(!is_string($name) || !is_string($reference) || preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D',$name)!==1 || preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D',$reference)!==1){throw new \InvalidArgumentException('HTTP destinations require named scoped secret references.');}
        }
        $this->httpDestinations=$destinations;
        if (!is_array($document['operations'] ?? null)) {throw new \InvalidArgumentException('Approved operations are required.');}
        $this->operations=new ApprovedOperations($document['operations']);
        $accounts=$document['accounts'] ?? null;
        if (!is_array($accounts) || !array_is_list($accounts) || $accounts===[]) {throw new \InvalidArgumentException('Explicit WorkFlow accounts are required.');}
        $definitions=[];$grants=[];$httpRequirements=[];
        foreach($accounts as $account) {
            if (!is_array($account) || array_diff(array_keys($account),['id','definitions','grants'])!==[]
                || !is_string($account['id'] ?? null) || $account['id']==='' || strlen($account['id'])>200
                || isset($definitions[$account['id']]) || !is_array($account['definitions'] ?? null)
                || !is_array($account['grants'] ?? null) || !array_is_list($account['grants'])) {
                throw new \InvalidArgumentException('Invalid WorkFlow account projection.');
            }
            $definitions[$account['id']]=new PublishedDefinitions($account['definitions'],$this->operations);
            foreach($account['definitions'] as $definition){foreach($definition['steps']??[] as $step){
                if(($step['metadata']['operation']??null)==='gateway.http.v1'){
                    $name=$step['metadata']['destination'];
                    if(!isset($destinations[$name])){throw new \InvalidArgumentException('HTTP step selects an unavailable destination binding.');}
                    $httpRequirements[]=['destination'=>$name,'fingerprint'=>$step['metadata']['destination_sha256'],'idempotent'=>($step['retry_policy']['idempotent']??false)===true];
                }
            }}
            foreach($account['grants'] as $documentGrant) {
                if (!is_array($documentGrant)) {throw new \InvalidArgumentException('Invalid WorkFlow grant.');}
                $grant=RunGrant::fromArray($documentGrant);
                if ($grant->account!==$account['id']) {throw new \InvalidArgumentException('A grant cannot cross account ownership.');}
                $definitions[$account['id']]->definition($grant->flowKey);
                $grants[]=$grant;
            }
        }
        $this->httpRequirements=$httpRequirements;
        $this->definitions=$definitions;
        $this->grants=$grants;
    }
    /** Validate the resolved secret before any worker can claim work. */
    public function assertAuthentication(array $credentials): void
    {
        if (count($credentials) !== count($this->definitions)) {
            throw new \InvalidArgumentException('WorkFlow accounts and credentials do not match.');
        }
        $seen=[];
        foreach ($this->definitions as $account=>$definition) {
            $token=$credentials[$account] ?? null;
            if (!is_string($token) || strlen($token)<32 || isset($seen[$token])) {
                throw new \InvalidArgumentException('Invalid or ambiguous WorkFlow account credentials.');
            }
            $seen[$token]=true;
        }
    }

}
