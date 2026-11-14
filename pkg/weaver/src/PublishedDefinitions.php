<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Services\FlowDefinitionValidator;
use Webong\WorkFlow\ValueObjects\FlowDefinition;

/** One account's immutable, explicitly selected definition versions. */
final readonly class PublishedDefinitions implements FlowDefinitionProvider
{
    /** @var array<string, FlowDefinition> */
    private array $definitions;

    /** @param list<array<string,mixed>> $documents */
    public function __construct(array $documents, ApprovedOperations $operations)
    {
        $definitions=[];
        $validator=new FlowDefinitionValidator([$operations],[]);
        foreach($documents as $document) {
            if (!is_int($document['version'] ?? null) || $document['version'] < 1 || !$validator->validate($document)->isValid()) {
                throw new \InvalidArgumentException('Published WorkFlow definitions require a valid explicit version and approved operations.');
            }
            $definition=FlowDefinition::fromArray($document);
            if (isset($definitions[$definition->key])) {throw new \InvalidArgumentException('A runtime must select exactly one version of each flow.');}
            $definitions[$definition->key]=$definition;
        }
        $this->definitions=$definitions;
    }

    public function definition(string $flowKey): FlowDefinition
    {
        return $this->definitions[$flowKey] ?? throw new RpcFailure(-32004,'Flow definition not found');
    }
}
