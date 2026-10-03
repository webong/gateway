<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use RuntimeException;
use stdClass;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailRegistry;
use Webong\Gateway\Email\VerificationRecordMissing;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class EmailController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private EmailRegistry $registry,
    ) {}

    public function domains(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return response()->json(['data' => array_map($this->domainResource(...), $this->registry->domains())]);
    }

    public function createDomain(Request $request): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['domain']) !== []) {
            return $this->invalid('The email domain is invalid.');
        }
        if (! is_string($input['domain'] ?? null)) {
            return $this->invalid('A valid DNS domain is required.');
        }
        $name = strtolower(rtrim(trim($input['domain']), '.'));
        if (preg_match('/^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])$/', $name) !== 1) {
            return $this->invalid('A valid DNS domain is required.');
        }
        try {
            $domain = $this->registry->createDomain($name);
        } catch (QueryException $exception) {
            if ($this->registry->domainByName($name) !== null) {
                return response()->json(['message' => 'The email domain already exists.'], Response::HTTP_CONFLICT);
            }
            throw $exception;
        }

        return response()->json($this->domainResource($domain), Response::HTTP_CREATED);
    }

    public function showDomain(Request $request, string $domain): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }

        return ($model = $this->registry->domain($domain)) === null
            ? $this->missing()
            : response()->json($this->domainResource($model));
    }

    public function verifyDomain(Request $request, string $domain): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null) {
            return $this->missing();
        }
        try {
            return response()->json($this->domainResource($this->registry->verify($model)));
        } catch (VerificationRecordMissing $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (RuntimeException) {
            return response()->json(['message' => 'DNS verification lookup is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }

    public function updateDomain(Request $request, string $domain): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null) {
            return $this->missing();
        }
        $input = $request->json()->all();
        if (array_keys($input) !== ['enabled'] || ! is_bool($input['enabled'])) {
            return $this->invalid('Only a boolean enabled value may be updated.');
        }
        try {
            return response()->json($this->domainResource($this->registry->setEnabled($model, $input['enabled'])));
        } catch (VerificationRecordMissing $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (RuntimeException) {
            return response()->json(['message' => 'DNS verification lookup is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
        }
    }

    public function deleteDomain(Request $request, string $domain): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null) {
            return $this->missing();
        }
        $this->registry->deleteDomain($model);

        return response()->noContent();
    }

    public function aliases(Request $request, string $domain): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null) {
            return $this->missing();
        }

        return response()->json(['data' => array_map($this->aliasResource(...), $this->registry->aliases($model))]);
    }

    public function createAlias(Request $request, string $domain): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null) {
            return $this->missing();
        }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['local_part', 'recipients']) !== [] || ! is_string($input['local_part'] ?? null)) {
            return $this->invalid('The email alias is invalid.');
        }
        $localPart = strtolower($input['local_part']);
        if (! $this->validLocalPart($localPart) || ! $this->validRecipients($input['recipients'] ?? null)) {
            return $this->invalid('Provide a valid local part, prefix* pattern, or * catch-all and 1–10 unique recipient addresses.');
        }
        try {
            $alias = $this->registry->createAlias($model, $localPart, $input['recipients']);
        } catch (QueryException $exception) {
            if ($this->registry->aliasByLocalPart($model, $localPart) !== null) {
                return response()->json(['message' => 'The email alias already exists.'], Response::HTTP_CONFLICT);
            }
            throw $exception;
        }

        return response()->json($this->aliasResource($alias), Response::HTTP_CREATED);
    }

    public function showAlias(Request $request, string $domain, string $alias): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null || ($record = $this->registry->alias($model, $alias)) === null) {
            return $this->missing();
        }

        return response()->json($this->aliasResource($record));
    }

    public function updateAlias(Request $request, string $domain, string $alias): JsonResponse
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null || ($record = $this->registry->alias($model, $alias)) === null) {
            return $this->missing();
        }
        $input = $request->json()->all();
        if ($input === [] || array_diff(array_keys($input), ['recipients', 'enabled']) !== []
            || (array_key_exists('recipients', $input) && ! $this->validRecipients($input['recipients']))
            || (array_key_exists('enabled', $input) && ! is_bool($input['enabled']))) {
            return $this->invalid('The email alias update is invalid.');
        }

        return response()->json($this->aliasResource($this->registry->updateAlias($model, $record, $input)));
    }

    public function deleteAlias(Request $request, string $domain, string $alias): Response
    {
        if (($denied = $this->authenticator->authorize($request)) !== null) {
            return $denied;
        }
        $model = $this->registry->domain($domain);
        if ($model === null || ($record = $this->registry->alias($model, $alias)) === null) {
            return $this->missing();
        }
        $this->registry->deleteAlias($record);

        return response()->noContent();
    }

    /** @return array<string, mixed> */
    private function domainResource(stdClass $domain): array
    {
        return [
            'id' => $domain->id,
            'domain' => $domain->domain,
            'enabled' => (bool) $domain->enabled,
            'verified_at' => $domain->verified_at,
            'verification' => [
                'name' => '_gateway-verify.'.$domain->domain,
                'type' => 'TXT',
                'value' => 'gateway-verification='.$domain->verification_token,
            ],
        ];
    }

    /** @return array<string, mixed> */
    private function aliasResource(stdClass $alias): array
    {
        return [
            'id' => $alias->id,
            'domain_id' => $alias->domain_id,
            'local_part' => $alias->local_part,
            'recipients' => json_decode($alias->recipients, true, 512, JSON_THROW_ON_ERROR),
            'enabled' => (bool) $alias->enabled,
        ];
    }

    private function validLocalPart(string $value): bool
    {
        if ($value === '*') {
            return true;
        }

        $literal = str_ends_with($value, '*') ? substr($value, 0, -1) : $value;

        if (preg_match('/^srs[01]-/i', $literal) === 1) {
            return false;
        }

        return strlen($value) <= 64 && preg_match('/^[a-z0-9][a-z0-9._+-]*$/', $literal) === 1
            && ! str_ends_with($literal, '.') && ! str_contains($literal, '..');
    }

    private function validRecipients(mixed $value): bool
    {
        if (! is_array($value) || ! array_is_list($value) || count($value) < 1 || count($value) > 10) {
            return false;
        }
        $validator = $this->validator->make(['recipients' => $value], [
            'recipients.*' => ['required', 'string', 'email', 'max:254', 'distinct'],
        ]);

        if ($validator->fails()) {
            return false;
        }
        foreach ($value as $address) {
            $separator = strrpos($address, '@');
            if ($separator !== false && $this->registry->domainByName(strtolower(substr($address, $separator + 1))) !== null) {
                return false;
            }
        }

        return true;
    }

    private function missing(): JsonResponse
    {
        return response()->json(['message' => 'Email resource not found.'], Response::HTTP_NOT_FOUND);
    }

    private function invalid(string $message): JsonResponse
    {
        return response()->json(['message' => $message], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
