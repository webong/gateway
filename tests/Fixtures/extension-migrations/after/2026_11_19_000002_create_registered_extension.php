<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        foreach (['accounts', 'adapter_prerequisites', 'extension_probes'] as $table) {
            if (! Schema::hasTable($table)) {
                throw new RuntimeException('Missing dependency: '.$table);
            }
        }
        Schema::create('registered_extension_rows', fn (Blueprint $table) => $table->string('value'));
    }
};
