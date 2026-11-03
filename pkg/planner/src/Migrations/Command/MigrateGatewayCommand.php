<?php

declare(strict_types=1);

namespace Webong\Gateway\Migrations\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Webong\Gateway\Migrations\GatewayMigrationRunner;

#[AsCommand(name: 'gateway:migrate', description: 'Apply the framework-neutral Gateway schema migrations')]
final class MigrateGatewayCommand extends Command
{
    public function __construct(private readonly GatewayMigrationRunner $runner)
    {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this->addOption(
            'adopt-existing',
            null,
            InputOption::VALUE_NONE,
            'Validate and record an existing Laravel-managed Gateway schema without replaying it',
        );
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $result = $input->getOption('adopt-existing')
            ? $this->runner->adoptExistingSchema()
            : $this->runner->migrate();

        if ($result->applied === []) {
            $output->writeln('<info>Gateway schema is already current.</info>');

            return self::SUCCESS;
        }

        $output->writeln(sprintf(
            '<info>Applied %d Gateway migration%s.</info>',
            count($result->applied),
            count($result->applied) === 1 ? '' : 's',
        ));

        return self::SUCCESS;
    }
}
