<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('mailbox_oauth_transactions', static function (Blueprint $table): void {
            $table->string('state_hash', 64)->primary();
            $table->string('account_id', 200)->index();
            $table->longText('ciphertext');
            $table->string('status', 16)->default('pending');
            $table->string('code_hash', 64)->nullable();
            $table->longText('result_ciphertext')->nullable();
            $table->timestamp('expires_at')->index();
            $table->timestamps();
            $table->foreign('account_id')->references('id')->on('accounts')->cascadeOnDelete();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('mailbox_oauth_transactions');
    }
};
