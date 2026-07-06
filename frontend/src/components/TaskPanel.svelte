<script lang="ts">
    import type { SessionState, TodoItem } from '../stores/sessions';

    export let session: SessionState;

    let showPrompt = false;

    $: todos = (session.todos ?? []) as TodoItem[];
    $: total = todos.length;
    $: completed = todos.filter((t) => t.status === 'completed').length;
    // Count the in_progress item as half done so the bar moves as soon as
    // Claude starts a step, not only when it finishes one.
    $: inProgress = todos.filter((t) => t.status === 'in_progress').length;
    $: percent = total > 0 ? Math.round(((completed + inProgress * 0.5) / total) * 100) : 0;

    $: currentLabel =
        session.current_task ||
        (session.status === 'working' ? 'Working…' : '');

    function icon(t: TodoItem): string {
        if (t.status === 'completed') return '✓';
        if (t.status === 'in_progress') return '▸';
        return '○';
    }

    function label(t: TodoItem): string {
        return t.status === 'in_progress' && t.activeForm ? t.activeForm : t.content;
    }
</script>

<div class="w-72 shrink-0 border-l border-bg-border bg-bg-panel flex flex-col min-h-0">
    <div class="px-3 py-2 border-b border-bg-border flex items-center justify-between">
        <span class="text-xs font-semibold text-text uppercase tracking-wide">Task</span>
        {#if total > 0}
            <span class="text-xs text-text-muted">{completed}/{total} · {percent}%</span>
        {/if}
    </div>

    <div class="flex-1 min-h-0 overflow-y-auto px-3 py-2 space-y-3">
        {#if total > 0}
            <!-- Progress bar -->
            <div class="h-1.5 rounded bg-bg-elevated overflow-hidden">
                <div
                    class="h-full rounded bg-status-working transition-all duration-500"
                    style="width: {percent}%"></div>
            </div>
        {/if}

        {#if currentLabel}
            <div>
                <div class="text-[10px] uppercase tracking-wide text-text-muted mb-0.5">Now</div>
                <div class="text-xs text-text break-words">{currentLabel}</div>
            </div>
        {/if}

        {#if total > 0}
            <ul class="space-y-1">
                {#each todos as t}
                    <li class="flex items-start gap-1.5 text-xs leading-snug">
                        <span
                            class:text-status-working={t.status === 'in_progress'}
                            class:text-text-muted={t.status !== 'in_progress'}
                            class="shrink-0 w-3 text-center">{icon(t)}</span>
                        <span
                            class:line-through={t.status === 'completed'}
                            class:text-text-muted={t.status === 'completed'}
                            class:text-text={t.status !== 'completed'}
                            class="break-words">{label(t)}</span>
                    </li>
                {/each}
            </ul>
        {:else if !currentLabel}
            <div class="text-xs text-text-muted">
                No task activity yet — the plan appears here once the session
                starts working.
            </div>
        {/if}

        {#if (session.prompt ?? '').trim()}
            <div class="pt-2 border-t border-bg-border">
                <button
                    type="button"
                    class="text-[10px] uppercase tracking-wide text-text-muted hover:text-text"
                    on:click={() => (showPrompt = !showPrompt)}>
                    {showPrompt ? '− ' : '+ '}Session prompt
                </button>
                {#if showPrompt}
                    <div class="mt-1 text-xs text-text-muted whitespace-pre-wrap break-words">
                        {session.prompt}
                    </div>
                {/if}
            </div>
        {/if}
    </div>
</div>
