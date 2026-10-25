<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use Webong\Gateway\Email\EmailVerificationProfile;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class EmailVerificationProfileController
{
    public function __construct(private RegistryRequestAuthenticator $auth,
        private EmailVerificationProfile $profiles) {}

    public function show(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $row = $this->profiles->find();
        return response()->json($row === null
            ? [...$this->profiles->current(), 'revision' => null]
            : [...$row->document, 'revision' => $row->revision])
            ->header('Cache-Control', 'no-store')
            ->header('ETag', ResourcePrecondition::etag($row?->revision ?? 0));
    }

    public function store(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        try { $row = $this->profiles->create($request->json()->all()); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid Email verification profile.'], 422); }
        catch (QueryException) { return response()->json(['message' => 'Email verification profile already exists.'], 409); }
        return response()->json([...$row->document, 'revision' => $row->revision], 201)
            ->header('ETag', ResourcePrecondition::etag($row->revision));
    }

    public function replace(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) { return response()->json(['message' => 'A current If-Match revision is required.'], 428); }
        try { $row = $this->profiles->replace($request->json()->all(), $revision); }
        catch (InvalidArgumentException) { return response()->json(['message' => 'Invalid Email verification profile.'], 422); }
        catch (StaleResource) { return response()->json(['message' => 'Email verification profile has changed.'], 412); }
        return $row === null ? response()->json(['message' => 'Email verification profile not found.'], 404)
            : response()->json([...$row->document, 'revision' => $row->revision])
                ->header('ETag', ResourcePrecondition::etag($row->revision));
    }
}
