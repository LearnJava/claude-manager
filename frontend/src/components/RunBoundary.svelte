<script lang="ts">
    // Divider drawn above the first line of a process launch (`run_start`,
    // UI-14) in both log layouts. The header of a saved previous run
    // (source "history", stores/sessions.ts loadPreviousRunLog) gets its own label.
    import type { LogEntry } from '../stores/sessions';
    import { t } from '../lib/i18n';
    import { formatTime } from '../lib/formatters';

    export let entry: LogEntry;
</script>

<div class="flex items-center gap-2 py-2 text-xs text-text-muted" data-testid="run-boundary">
    <div class="flex-1 border-t border-border"></div>
    <span>
        {entry.source === 'history' ? $t('logStream.previousRunLabel') : $t('logStream.newRun')}
        · {formatTime(entry.time)}
    </span>
    <div class="flex-1 border-t border-border"></div>
</div>
