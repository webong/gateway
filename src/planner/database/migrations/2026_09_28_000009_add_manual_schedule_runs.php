<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::table('schedule_runs', function (Blueprint $table): void {
            $table->string('kind')->default('cron');
            $table->string('occurrence_key')->nullable();
        });

        DB::table('schedule_runs')->orderBy('id')->chunk(100, static function ($runs): void {
            foreach ($runs as $run) {
                DB::table('schedule_runs')->where('id', $run->id)->update([
                    'occurrence_key' => 'cron:'.$run->scheduled_for,
                ]);
            }
        });

        Schema::table('schedule_runs', function (Blueprint $table): void {
            $table->dropUnique(['schedule_id', 'scheduled_for']);
            $table->unique(['schedule_id', 'occurrence_key']);
        });
    }

    public function down(): void
    {
        $hasDuplicates = DB::table('schedule_runs')->select('schedule_id', 'scheduled_for')
            ->groupBy('schedule_id', 'scheduled_for')->havingRaw('count(*) > 1')->exists();
        if ($hasDuplicates) {
            throw new RuntimeException('Cannot roll back manual schedule runs while multiple runs share an occurrence time.');
        }

        Schema::table('schedule_runs', function (Blueprint $table): void {
            $table->dropUnique(['schedule_id', 'occurrence_key']);
            $table->dropColumn(['kind', 'occurrence_key']);
            $table->unique(['schedule_id', 'scheduled_for']);
        });
    }
};
