<script lang="ts">
    import { onMount, onDestroy } from 'svelte';
    import { sessions, type SessionState, type PermissionRequest } from '../stores/sessions';
    import { RespondPermission } from '../../wailsjs/go/main/App';

    export let session: SessionState;

    // 1s ticker so the "waiting (Ns)" label and the 30s flash trigger update live.
    let now = Date.now();
    let tick: ReturnType<typeof setInterval>;

    onMount(() => {
        tick = setInterval(() => (now = Date.now()), 1000);
    });
    onDestroy(() => {
        if (tick) clearInterval(tick);
    });

    let busy: string | null = null;
    let error = '';

    // Read both snake_case (JSON from Go) and PascalCase (Wails generated model) field names.
    function pick(req: PermissionRequest | null | undefined, ...keys: string[]): string {
        if (!req) return '';
        for (const k of keys) {
            const v = (req as any)[k];
            if (v !== undefined && v !== null && v !== '') return String(v);
        }
        return '';
    }

    $: req = session.pending_permission ?? null;
    $: requestId = pick(req, 'id', 'request_id', 'ID', 'RequestID');
    $: tool = pick(req, 'tool', 'Tool');
    $: command = pick(req, 'command', 'Command');
    $: filePath = pick(req, 'file_path', 'file', 'FilePath', 'File');
    $: description = pick(req, 'description', 'Description');
    $: risk = (pick(req, 'risk_level', 'RiskLevel') || 'low').toLowerCase();
    $: waitingSinceStr = pick(req, 'waiting_since', 'WaitingSince', 'timestamp', 'Timestamp');
    $: waitingSinceMs = waitingSinceStr ? new Date(waitingSinceStr).getTime() : now;
    $: waitedSec = Math.max(0, Math.floor((now - (waitingSinceMs || now)) / 1000));
    $: flash = waitedSec >= 30;
    $: target = command || filePath || description || '(no details)';

    function riskColorClass(r: string): string {
        if (r === 'high') return 'text-status-error';
        if (r === 'medium') return 'text-status-ratelimit';
        return 'text-status-working';
    }

    function riskBorderClass(r: string): string {
        if (r === 'high') return 'border-status-error';
        if (r === 'medium') return 'border-status-ratelimit';
        return 'border-status-waiting';
    }

    function riskIcon(r: string): string {
        if (r === 'high') return '🔴';
        if (r === 'medium') return '🟡';
        return '🟢';
    }

    function riskLabel(r: string): string {
        if (r === 'high') return 'high';
        if (r === 'medium') return 'medium';
        return 'low';
    }

    function formatWait(sec: number): string {
        if (sec < 60) return `${sec}s`;
        const m = Math.floor(sec / 60);
        const s = sec % 60;
        return `${m}m ${s}s`;
    }

    async function respond(decision: string, label: string) {
        if (busy || !requestId) return;
        busy = label;
        error = '';
        try {
            await RespondPermission(session.id, requestId, decision);
            // Optimistically clear the banner — the backend doesn't emit a
            // "permission cleared" event, only a subsequent status change.
            sessions.update((map) => {
                const cur = map[session.id];
                if (!cur) return map;
                return { ...map, [session.id]: { ...cur, pending_permission: null } };
            });
        } catch (e: any) {
            error = `${label} failed: ${e?.message ?? String(e)}`;
        } finally {
            busy = null;
        }
    }
</script>

{#if req}
    <div
        class="permission-banner border-l-4 {riskBorderClass(risk)}
               bg-bg-elevated border-t border-r border-b border-bg-border
               px-3 py-2 m-2 rounded shadow
               {flash ? 'banner-flash' : ''}"
        role="alertdialog"
        aria-live="assertive">
        <!-- Header: title + waiting time -->
        <div class="flex items-center gap-2 text-sm">
            <span class="text-status-waiting">⚠</span>
            <span class="font-semibold text-text">
                {session.name} is waiting for permission
            </span>
            <span class="text-text-muted text-xs ml-auto tabular-nums" title="Time waited">
                {formatWait(waitedSec)}
            </span>
        </div>

        <!-- Body: tool / target / risk -->
        <div class="mt-1.5 text-sm flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-text-muted">🔧</span>
            <span class="font-mono text-text font-semibold">{tool || '(unknown)'}</span>
            {#if target && target !== '(no details)'}
                <span class="text-text-muted">:</span>
                <span class="font-mono text-text break-all">{target}</span>
            {/if}
        </div>

        {#if description && description !== target}
            <div class="mt-1 text-xs text-text-muted break-words">{description}</div>
        {/if}

        <div class="mt-1 text-xs">
            <span class="text-text-muted">Risk:</span>
            <span class="ml-1">{riskIcon(risk)}</span>
            <span class="ml-1 {riskColorClass(risk)} font-medium">{riskLabel(risk)}</span>
        </div>

        {#if error}
            <div class="mt-1 text-xs text-status-error">{error}</div>
        {/if}

        <!-- Action buttons -->
        <div class="mt-2 flex flex-wrap gap-2">
            <button
                type="button"
                on:click={() => respond('allow', 'Allow')}
                disabled={!!busy}
                class="px-2.5 py-1 text-xs rounded font-medium
                       bg-status-working/90 hover:bg-status-working text-white
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy === 'Allow' ? '…' : '✓ Allow'}
            </button>
            <button
                type="button"
                on:click={() => respond('deny', 'Deny')}
                disabled={!!busy}
                class="px-2.5 py-1 text-xs rounded font-medium
                       bg-status-error/90 hover:bg-status-error text-white
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy === 'Deny' ? '…' : '✗ Deny'}
            </button>
            <button
                type="button"
                on:click={() => respond('allow_similar', 'Allow similar')}
                disabled={!!busy}
                title="Allow this and similar requests until the manager restarts"
                class="px-2.5 py-1 text-xs rounded
                       bg-bg-panel border border-bg-border text-text hover:bg-bg
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy === 'Allow similar' ? '…' : '✓ Allow similar'}
            </button>
            <button
                type="button"
                on:click={() => respond('allow_always', 'Always allow')}
                disabled={!!busy}
                title="Allow + persist a rule in the config"
                class="px-2.5 py-1 text-xs rounded
                       bg-bg-panel border border-bg-border text-text hover:bg-bg
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy === 'Always allow' ? '…' : '✓ Always allow'}
            </button>
            <button
                type="button"
                on:click={() => respond('deny_always', 'Always deny')}
                disabled={!!busy}
                title="Deny + persist a rule in the config"
                class="px-2.5 py-1 text-xs rounded
                       bg-bg-panel border border-bg-border text-text hover:bg-bg
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {busy === 'Always deny' ? '…' : '✗ Always deny'}
            </button>
        </div>
    </div>
{/if}

<style>
    @keyframes permissionFlash {
        0%, 100% {
            background-color: rgb(28 32 48); /* bg-bg-elevated */
            box-shadow: 0 0 0 0 rgba(234, 179, 8, 0);
        }
        50% {
            background-color: rgb(60 50 10);
            box-shadow: 0 0 0 4px rgba(234, 179, 8, 0.35);
        }
    }
    .banner-flash {
        animation: permissionFlash 1s ease-in-out infinite;
    }
</style>
