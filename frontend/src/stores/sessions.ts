import { writable, derived, get } from 'svelte/store';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import {
    GetAllSessions,
    GetRateLimitStatus,
    GetDailyCost,
    Notify,
} from '../../wailsjs/go/main/App';

// ---- Types mirroring Go SessionState / event payloads ----

export type SessionStatus =
    | 'idle'
    | 'starting'
    | 'analyzing'
    | 'working'
    | 'waiting_permission'
    | 'rate_limited'
    | 'retrying'
    | 'stopping'
    | 'error'
    | 'unknown';

export interface PermissionRequest {
    request_id?: string;
    RequestID?: string;
    tool?: string;
    Tool?: string;
    command?: string;
    Command?: string;
    file?: string;
    File?: string;
    risk_level?: string;
    RiskLevel?: string;
    [k: string]: any;
}

export interface TodoItem {
    content: string;
    status: 'pending' | 'in_progress' | 'completed' | string;
    activeForm?: string;
}

export interface SessionState {
    id: string;
    project: string;
    name: string;
    status: SessionStatus;
    model: string;
    effort: string;
    permission_mode: string;
    started_at: string;
    last_activity: string;
    rate_limit_until: string;
    tasks_done: number;
    current_task: string;
    prompt: string;
    todos: TodoItem[];
    branch: string;
    cli_session_id: string;
    pending_permission?: PermissionRequest | null;
    input_tokens: number;
    output_tokens: number;
    cache_read: number;
    cache_creation: number;
    num_turns: number;
    total_cost_usd: number;
    context_window: number;
    context_util: number;
}

export interface LogEntry {
    time: string;
    level: string;
    source: string;
    message: string;
    tool_name?: string;
    tool_input?: string;
}

export interface RateLimitInfo {
    Status?: string;
    status?: string;
    Utilization?: number;
    utilization?: number;
    ResetsAt?: string;
    resets_at?: string;
    [k: string]: any;
}

// ---- Stores ----

// Map of session id -> SessionState
export const sessions = writable<Record<string, SessionState>>({});

// Currently selected session id (for Main Panel)
export const selectedSessionId = writable<string | null>(null);

// Global rate limit status (from manager)
export const rateLimitStatus = writable<RateLimitInfo | null>(null);

// Total cost for today (in USD)
export const todayCost = writable<number>(0);

// Track when the manager started (for uptime in the status bar)
export const appStartedAt = writable<Date>(new Date());

// Per-session log buffers (in-memory tail). Persisted logs come from store via Wails.
export const sessionLogs = writable<Record<string, LogEntry[]>>({});

// Maximum number of log entries kept in memory per session (UI buffer only).
const LOG_BUFFER_LIMIT = 2000;

// ---- Derived stores ----

export const sessionList = derived(sessions, ($s) => Object.values($s));

export const activeSessions = derived(sessionList, ($list) =>
    $list.filter(
        (s) =>
            s.status === 'working' ||
            s.status === 'starting' ||
            s.status === 'analyzing' ||
            s.status === 'retrying',
    ),
);

export const waitingSessions = derived(sessionList, ($list) =>
    $list.filter((s) => s.status === 'waiting_permission'),
);

export const rateLimitedSessions = derived(sessionList, ($list) =>
    $list.filter((s) => s.status === 'rate_limited'),
);

export const errorSessions = derived(sessionList, ($list) =>
    $list.filter((s) => s.status === 'error'),
);

// ---- Mutations ----

function makeBlankSession(id: string): SessionState {
    const slash = id.indexOf('/');
    const project = slash >= 0 ? id.slice(0, slash) : id;
    const name = slash >= 0 ? id.slice(slash + 1) : id;
    return {
        id, project, name,
        status: 'idle', model: '', effort: '', permission_mode: '',
        started_at: '', last_activity: '', rate_limit_until: '',
        tasks_done: 0, current_task: '', prompt: '', todos: [], branch: '', cli_session_id: '',
        pending_permission: null,
        input_tokens: 0, output_tokens: 0, cache_read: 0, cache_creation: 0,
        num_turns: 0, total_cost_usd: 0, context_window: 0, context_util: 0,
    };
}

async function refreshSessions(): Promise<void> {
    try {
        const list = (await GetAllSessions()) as SessionState[];
        sessions.update((map) => {
            const updated = { ...map };
            (list ?? []).forEach((s) => {
                if (!updated[s.id]) updated[s.id] = s;
            });
            return updated;
        });
    } catch { /* non-critical */ }
}

// Coalesce backend refreshes triggered by unknown-session events. A burst of
// events must never fire one GetAllSessions per event, and the refresh must run
// OUTSIDE the sessions.update() callback — calling sessions.update() from within
// another sessions.update() re-renders every subscriber synchronously on every
// event, which detaches sidebar rows mid-interaction and saturates the renderer.
let refreshScheduled = false;
function scheduleRefresh() {
    if (refreshScheduled) return;
    refreshScheduled = true;
    setTimeout(() => {
        refreshScheduled = false;
        void refreshSessions();
    }, 0);
}

function setSession(id: string, mutator: (s: SessionState) => SessionState) {
    let unknown = false;
    sessions.update((map) => {
        const existing = map[id];
        if (!existing) {
            // First event for an unknown session: create a blank entry immediately
            // so the UI can render; full state is fetched once, debounced, below.
            unknown = true;
            return { ...map, [id]: mutator(makeBlankSession(id)) };
        }
        return { ...map, [id]: mutator(existing) };
    });
    if (unknown) scheduleRefresh();
}

function appendLog(id: string, entry: LogEntry) {
    sessionLogs.update((map) => {
        const cur = map[id] ?? [];
        const next = cur.length >= LOG_BUFFER_LIMIT
            ? [...cur.slice(cur.length - LOG_BUFFER_LIMIT + 1), entry]
            : [...cur, entry];
        return { ...map, [id]: next };
    });
}

export function clearSessionLog(id: string) {
    sessionLogs.update((m) => ({ ...m, [id]: [] }));
}

// ---- Bootstrap: load initial state + subscribe to Wails events ----

let initialized = false;

export async function initSessions(): Promise<void> {
    if (initialized) return;
    initialized = true;

    appStartedAt.set(new Date());

    try {
        const list = (await GetAllSessions()) as SessionState[];
        const map: Record<string, SessionState> = {};
        (list ?? []).forEach((s) => {
            map[s.id] = s;
        });
        sessions.set(map);
    } catch (e) {
        console.warn('GetAllSessions failed:', e);
    }

    try {
        const info = (await GetRateLimitStatus()) as RateLimitInfo | null;
        rateLimitStatus.set(info);
    } catch (e) {
        console.warn('GetRateLimitStatus failed:', e);
    }

    refreshTodayCost();

    // ---- Wails event subscriptions ----

    EventsOn('session:status', (evt: { id: string; status: SessionStatus }) => {
        if (!evt || !evt.id) return;
        const clearPerm = (['idle', 'error', 'stopping'] as SessionStatus[]).includes(evt.status);
        setSession(evt.id, (s) => ({
            ...s,
            status: evt.status,
            pending_permission: clearPerm ? null : s.pending_permission,
        }));
    });

    EventsOn('session:log', (evt: { id: string; entry: LogEntry }) => {
        if (!evt || !evt.id) return;
        appendLog(evt.id, evt.entry);
        setSession(evt.id, (s) => ({ ...s, last_activity: evt.entry?.time ?? s.last_activity }));
    });

    EventsOn('session:task_done', (evt: { id: string; tasks_done: number }) => {
        if (!evt || !evt.id) return;
        setSession(evt.id, (s) => ({ ...s, tasks_done: evt.tasks_done }));
        // Refresh cost on task completion since each result event finalizes cost.
        refreshTodayCost();
        notify('Task complete', `${evt.id} — ${evt.tasks_done} task(s) done`);
    });

    EventsOn('session:rate_limit', (evt: { id: string; info: RateLimitInfo; until: string }) => {
        if (!evt) return;
        rateLimitStatus.set(evt.info ?? null);
        if (evt.id) {
            setSession(evt.id, (s) => ({ ...s, rate_limit_until: evt.until }));
        }
        notify('Rate limited', `${evt.id ?? 'session'} — paused until ${evt.until ?? 'reset'}`);
    });

    EventsOn('session:permission', (evt: { id: string; request: PermissionRequest }) => {
        if (!evt || !evt.id) return;
        setSession(evt.id, (s) => ({ ...s, pending_permission: evt.request }));
        const tool = evt.request?.tool ?? evt.request?.Tool ?? 'tool';
        notify('Permission needed', `${evt.id}: ${tool}`);
    });

    EventsOn('session:context', (evt: {
        id: string;
        input_tokens: number;
        output_tokens: number;
        cache_read: number;
        cache_creation: number;
        context_window: number;
        utilization: number;
    }) => {
        if (!evt || !evt.id) return;
        setSession(evt.id, (s) => ({
            ...s,
            input_tokens: evt.input_tokens,
            output_tokens: evt.output_tokens,
            cache_read: evt.cache_read,
            cache_creation: evt.cache_creation,
            context_window: evt.context_window,
            context_util: evt.utilization,
        }));
    });

    EventsOn('session:todo', (evt: { id: string; todos: TodoItem[] | null; current_task: string }) => {
        if (!evt || !evt.id) return;
        setSession(evt.id, (s) => ({
            ...s,
            todos: evt.todos ?? [],
            current_task: evt.current_task ?? '',
        }));
    });

    EventsOn('session:init', (evt: { id: string; info: { model?: string; session_id?: string } }) => {
        if (!evt || !evt.id) return;
        setSession(evt.id, (s) => ({
            ...s,
            model: evt.info?.model ?? s.model,
            cli_session_id: evt.info?.session_id ?? s.cli_session_id,
        }));
    });

    EventsOn('session:result', (evt: { id: string; result: { total_cost_usd?: number } }) => {
        if (!evt || !evt.id) return;
        const cost = evt.result?.total_cost_usd ?? 0;
        setSession(evt.id, (s) => ({ ...s, total_cost_usd: s.total_cost_usd + cost }));
    });

    EventsOn('session:error', (evt: { id: string; message: string }) => {
        if (evt?.id) {
            notify('Session error', `${evt.id}: ${evt.message ?? 'unknown error'}`);
        }
    });
}

// Best-effort Windows toast — silently skip if the binding fails (e.g. on a
// platform without toast support).
function notify(title: string, body: string) {
    Notify(title, body).catch(() => {
        /* notifications are non-critical */
    });
}

async function refreshTodayCost() {
    try {
        const today = new Date().toISOString().slice(0, 10); // YYYY-MM-DD
        const cost = await GetDailyCost(today);
        todayCost.set(cost ?? 0);
    } catch (e) {
        // store may not be ready on first launch; ignore.
    }
}

// Helper for components — returns a snapshot of all sessions.
export function snapshot(): SessionState[] {
    return Object.values(get(sessions));
}
