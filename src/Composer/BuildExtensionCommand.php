<?php

declare(strict_types=1);

namespace Webong\GatewayBuilder\Composer;

use Composer\Command\BaseCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\OutputInterface;
use Webong\GatewayBuilder\PackageBuilder;

final class BuildExtensionCommand extends BaseCommand
{
    protected function configure(): void
    {
        $this->setName('gy:build')
            ->setDescription('Build a deterministic ext.package/v1 Gateway extension')
            ->addOption('manifest', null, InputOption::VALUE_REQUIRED)
            ->addOption('extension-version', null, InputOption::VALUE_REQUIRED)
            ->addOption('revision', null, InputOption::VALUE_OPTIONAL)
            ->addOption('source', null, InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY)
            ->addOption('value', null, InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY)
            ->addOption('output', null, InputOption::VALUE_REQUIRED);
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        foreach (['manifest', 'extension-version', 'output'] as $required) {
            if (! is_string($input->getOption($required)) || $input->getOption($required) === '') {
                throw new \InvalidArgumentException("Missing --{$required}.");
            }
        }

        $sourceRoots = [];
        foreach ($input->getOption('source') as $source) {
            if (! is_string($source) || ! str_contains($source, '=')) {
                throw new \InvalidArgumentException('Every --source must use NAME=DIR.');
            }
            [$name, $path] = explode('=', $source, 2);
            $sourceRoots[$name] = $path;
        }
        $values = [];
        foreach ($input->getOption('value') as $value) {
            if (! is_string($value) || ! str_contains($value, '=')) {
                throw new \InvalidArgumentException('Every --value must use NAME=VALUE.');
            }
            [$name, $replacement] = explode('=', $value, 2);
            $values[$name] = $replacement;
        }

        (new PackageBuilder)->build(
            manifestPath: $input->getOption('manifest'),
            version: $input->getOption('extension-version'),
            output: $input->getOption('output'),
            revision: is_string($input->getOption('revision')) ? $input->getOption('revision') : null,
            sourceRoots: $sourceRoots,
            values: $values,
        );
        $output->writeln($input->getOption('output'));

        return self::SUCCESS;
    }
}
