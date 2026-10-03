<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('email_http_submissions', function (Blueprint $table): void {
            $table->char('identity_hash', 64);
            $table->char('key_hash', 64);
            $table->char('request_hash', 64);
            $table->char('receipt_id', 32);
            $table->string('node', 64);
            $table->timestamp('accepted_at')->nullable();
            $table->timestamp('expires_at');
            $table->timestamps();

            $table->primary(['identity_hash', 'key_hash']);
            $table->index('expires_at');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('email_http_submissions');
    }
};
