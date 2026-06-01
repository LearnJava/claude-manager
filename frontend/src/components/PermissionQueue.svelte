<script lang="ts">
    import { onMount, onDestroy } from 'svelte';
    import {
        sessions,
        type SessionState,
        type PermissionRequest,
    } from '../stores/sessions';
    import { RespondPermission } from '../../wailsjs/go/main/App';

    // 1s ticker so per-row "waited" labels stay live.
    let now = Date.now();
    let tick: ReturnType<typeof setInterval>;

    onMount(() => {
        tick = setInterval(() => (now = Date.now()), 1000);
    });
    onDestroy(() => {
        if (tick) clearInterval(tick);
    });

    let busyId: string | null = null;
    let bulkBusy: string | null = null;
    let error = '';

    function pick(req: PermissionRequest | null | undefined, ...keys: string[]): string {
        if (!req) return '';
        for (const k of keys) {
            const v = (req as any)[k];
            if (v !== undefined && v !== null && v !== '') return String(v);
        }
        return '';
    }

    interface QueueEntry {
        session: SessionState;
        request: PermissionRequest;
        requestId: string;
        tool: string;
        target: string;
        risk: string;
        waitingMs: number;
    }

    function toEntry(s: SessionState, nowMs: number): QueueEntry | null {
        const req = s.pending_permission;
        if (!req) return null;
        const waitingSinceStr = pick(req, 'waiting_since', 'WaitingSince', 'timestamp', 'Timestamp');
        const waitingSinceMs = waitingSinceStr ? new Date(waitingSinceStr).getTime() : nowMs;
        const tool = pick(req, 'tool', 'Tool');
        const command = pick(req, 'command', 'Command');
        const filePath = pick(req, 'file_path', 'file', 'FilePath', 'File');
        const description = pick(req, 'description', 'Description');
        return {
            session: s,
            request: req,
            requestId: pick(req, 'id', 'request_id', 'ID', 'RequestID'),
            tool,
            target: command || filePath || description || '(no details)',
            risk: (pick(req, 'risk_level', 'RiskLevel') || 'low').toLowerCase(),
            waitingMs: Math.max(0, nowMs - (waitingSinceMs || nowMs)),
        };
    }

    // Reactive derived list — recomputes when `sessions` or `now` changes.
    $: entries = (Object.values($sessions) as SessionState[])
        .map((s) => toEntry(s, now))
        .filter((e): e is QueueEntry => e !== null)
        .sort((a, b) => b.waitingMs - a.waitingMs);

    function riskColorClass(r: string): string {
        if (r === 'high') return 'text-status-error';
        if (r === 'medium') return 'text-status-ratelimit';
        return 'text-status-working';
    }

    function riskIcon(r: string): string {
        if (r === 'high') return '🔴';
        if (r === 'medium') return '🟡';
        return '🟢';
    }

    function formatWait(ms: number): string {
        const sec = Math.max(0, Math.floor(ms / 1000));
        if (sec < 60) return `${sec}s`;
        const m = Math.floor(sec / 60);
        const s = sec % 60;
        if (m < 60) return `${m}m ${s}s`;
        const h = Math.floor(m / 60);
        return `${h}h ${m % 60}m`;
    }

    function clearLocal(sessionId: string) {
        sessions.update((map) => {
            const cur = map[sessionId];
            if (!cur) return map;
            return { ...map, [sessionId]: { ...cur, pending_permission: null } };
        });
    }

    async function respond(entry: QueueEntry, decision: string) {
        if (busyId || bulkBusy || !entry.requestId) return;
        busyId = `${entry.session.id}:${decision}`;
        error = '';
        try {
            await RespondPermission(entry.session.id, entry.requestId, decision);
            clearLocal(entry.session.id);
        } catch (e: any) {
            error = `Respond failed: ${e?.message ?? String(e)}`;
        } finally {
            busyId = null;
        }
    }

    async function bulk(filter: (e: QueueEntry) => boolean, decision: string, label: string) {
        if (busyId || bulkBusy) return;
        bulkBusy = label;
        error = '';
        const targets = entries.filter(filter);
        try {
            for (const e of targets) {
                if (!e.requestId) continue;
                try {
                    await RespondPermission(e.session.id, e.requestId, decision);
                    clearLocal(e.session.id);
                } catch (err: any) {
                    error = `${label}: ${err?.message ?? String(err)}`;
                }
            }
        } finally {
            bulkBusy = null;
        }
    }

    function allowAllSafe() {
        bulk((e) => e.risk !== 'high', 'allow', 'Allow all safe');
    }

    function denyAll() {
        bulk(() => true, 'deny', 'Deny all');
    }

    $: safeCount = entries.filter((e) => e.risk !== 'high').length;
</script>

<div class="flex flex-col min-h-0">
    <!-- Body: list -->
    <div class="flex-1 min-h-0 overflow-y-auto">
        {#if entries.length === 0}
            <div class="text-text-muted text-sm italic py-6 text-center">
                No pending permissions.
            </div>
        {:else}
            <ul class="divide-y divide-bg-border">
                {#each entries as e (e.session.id + ':' + e.requestId)}
                    <li class="py-2 px-1">
                        <div class="flex items-baseline gap-2 text-sm">
                            <span title={`Risk: ${e.risk}`}>{riskIcon(e.risk)}</span>
                            <span class="font-semibold text-text">{e.session.name}</span>
                            <span class="text-text-muted text-xs">— {e.session.project}</span>
                            <span class="text-text-muted text-xs ml-auto tabular-nums whitespace-nowrap"
                                title="Time waited">
                                {formatWait(e.waitingMs)}
                            </span>
                        </div>

                        <div class="mt-1 text-sm flex flex-wrap items-baseline gap-x-2">
                            <span class="font-mono text-text font-semibold">{e.tool || '(unknown)'}</span>
                            <span class="text-text-muted">:</span>
                            <span class="font-mono text-text break-all">{e.target}</span>
                        </div>

                        <div class="mt-1 text-xs">
                            <span class="text-text-muted">Risk:</span>
                            <span class="ml-1 {riskColorClass(e.risk)} font-medium">{e.risk}</span>
                        </div>

                        <div class="mt-2 flex flex-wrap gap-2">
                            <button
                                type="button"
                                on:click={() => respond(e, 'allow')}
                                disabled={!!busyId || !!bulkBusy}
                                class="px-2.5 py-1 text-xs rounded font-medium
                                       bg-status-working/90 hover:bg-status-working text-white
                                       disabled:opacity-50 disabled:cursor-not-allowed">
                                {busyId === `${e.session.id}:allow` ? '…' : '✓ Allow'}
                            </button>
                            <button
                                type="button"
                                on:click={() => respond(e, 'deny')}
                                disabled={!!busyId || !!bulkBusy}
                                class="px-2.5 py-1 text-xs rounded font-medium
                                       bg-status-error/90 hover:bg-status-error text-white
                                       disabled:opacity-50 disabled:cursor-not-allowed">
                                {busyId === `${e.session.id}:deny` ? '…' : '✗ Deny'}
                            </button>
                            <button
                                type="button"
                                on:click={() => respond(e, 'allow_similar')}
                                disabled={!!busyId || !!bulkBusy}
                                title="Allow and remember the pattern for this session"
                                class="px-2.5 py-1 text-xs rounded
                                       bg-bg-elevated border border-bg-border text-text hover:bg-bg
                                       disabled:opacity-50 disabled:cursor-not-allowed">
                                {busyId === `${e.session.id}:allow_similar` ? '…' : '✓ Allow similar'}
                            </button>
                        </div>
                    </li>
                {/each}
            </ul>
        {/if}
    </div>

    {#if error}
        <div class="text-status-error text-xs px-1 pt-2">{error}</div>
    {/if}

    <!-- Footer: quick actions -->
    {#if entries.length > 0}
        <div class="mt-3 pt-3 border-t border-bg-border flex flex-wrap items-center gap-2">
            <span class="text-text-muted text-xs mr-1">Quick actions:</span>
            <button
                type="button"
                on:click={allowAllSafe}
                disabled={!!busyId || !!bulkBusy || safeCount === 0}
                class="px-2.5 py-1 text-xs rounded font-medium
                       bg-status-working/90 hover:bg-status-working text-white
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {bulkBusy === 'Allow all safe' ? '…' : `✓ Allow all safe (${safeCount})`}
            </button>
            <button
                type="button"
                on:click={denyAll}
                disabled={!!busyId || !!bulkBusy}
                class="px-2.5 py-1 text-xs rounded font-medium
                       bg-status-error/90 hover:bg-status-error text-white
                       disabled:opacity-50 disabled:cursor-not-allowed">
                {bulkBusy === 'Deny all' ? '…' : `✗ Deny all (${entries.length})`}
            </button>
        </div>
    {/if}
</div>
