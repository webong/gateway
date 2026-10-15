<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('mailbox_accounts', function (Blueprint $table): void {
            $table->string('id', 255)->primary();
            $table->string('token_hash', 64);
            $table->timestamps();
        });
        Schema::create('mailbox_connections', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('account_id', 255);
            $table->string('address', 320);
            $table->string('imap_upstream_id', 64);
            $table->string('smtp_upstream_id', 64);
            $table->string('imap_username', 320);
            $table->string('smtp_username', 320);
            $table->string('imap_auth', 16);
            $table->string('smtp_auth', 16);
            $table->text('imap_secret');
            $table->text('smtp_secret');
            $table->string('imap_client_hash', 255);
            $table->string('smtp_client_hash', 255);
            $table->boolean('enabled')->default(false);
            $table->string('health', 32)->default('untested');
            $table->timestamp('tested_at')->nullable();
            $table->timestamps();
            $table->foreign('account_id')->references('id')->on('mailbox_accounts')->cascadeOnDelete();
            $table->unique(['account_id', 'address']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('mailbox_connections');
        Schema::dropIfExists('mailbox_accounts');
    }
};
