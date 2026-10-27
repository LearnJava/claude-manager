// Pure model of the "waiting for you" screen (WASTE-TASKS.md WS-08): every
// session that needs a person, across all projects, one row each, with the
// reason. Nothing here reads a store or the backend, so it is unit-testable;
// stores/waiting.ts feeds it and WaitingScreen.svelte renders it.
import type { SessionState } from '../stores/sessions';
import { hasQueue, type QueueModel } from './queue';

export type WaitingKind =
    | 'permission'
    | 'question'
    | 'gates_exhausted'
    | 'error'
    | 'rate_limit'
    | 'stopped'
    | 'queue_empty'
    | 'needs_human';

/** Most urgent first: a blocked session loses time every second it waits. */
const KIND_RANK: Record<WaitingKind, number> = {
    permission: 0,
    question: 1,
    gates_exhausted: 2,
    error: 3,
    needs_human: 4,
    rate_limit: 5,
    stopped: 6,
    queue_empty: 7,
};

export interface WaitingItem {
    /** Stable row key. */
    key: string;
    kind: WaitingKind;
    project: string;
    /** Session id ("project/name"); '' for a mixed task, which has no session. */
    sessionId: string;
    /** Short human detail: the tool, the question, the error text… May be empty. */
    detail: string;
    /** Epoch ms the wait began (or, for a rate limit, when it ends); null if unknown. */
    since: number | null;
    /** Mixed task id for a needs_human row. */
    taskId?: string;
}

/** A mixed-programming task that ended `needs_human` (worker.MixedTask subset). */
export interface HumanTask {
    ID: string;
    Project: string;
    WorkerName?: string;
    Error?: string;
}

export interface WaitingContext {
    /** Queue snapshots by session id (stores/queues.ts). */
    queues: Record<string, { model: QueueModel } | undefined>;
    /** Last `session:error` message by session id. */
    errors: Record<string, string>;
    mixed: HumanTask[];
}

/** The message strict gates (internal/session/session.go) stops with. */
export const GATES_EXHAUSTED_RE = /gates still red/i;

function ms(iso: string | undefined | null): number | null {
    if (!iso) return null;
    const t = Date.parse(iso);
    return Number.isFinite(t) && t > 0 ? t : null;
}

function permissionDetail(s: SessionState): string {
    const r = s.pending_permission as Record<string, any> | null | undefined;
    if (!r) return '';
    const tool = r.tool ?? r.Tool ?? '';
    const target = r.command ?? r.Command ?? r.file_path ?? r.file ?? r.FilePath ?? r.File ?? r.description ?? '';
    return [tool, target].filter(Boolean).join(': ');
}

function permissionSince(s: SessionState): number | null {
    const r = s.pending_permission as Record<string, any> | null | undefined;
    if (!r) return null;
    return ms(r.waiting_since ?? r.WaitingSince ?? r.timestamp ?? r.Timestamp);
}

/** A session ran a queue to its end: tasks were done, the roadmap has finished
 * rows, and nothing is left ahead. */
export function queueEmptied(s: SessionState, q: { model: QueueModel } | undefined): boolean {
    if (!q) return false;
    return (s.tasks_done ?? 0) > 0 && q.model.doneCount > 0 && !hasQueue(q.model);
}

function sessionRow(s: SessionState, ctx: WaitingContext): WaitingItem | null {
    const base = { project: s.project, sessionId: s.id };
    if (s.status === 'waiting_permission' || s.pending_permission) {
        return {
            ...base,
            key: `permission:${s.id}`,
            kind: 'permission',
            detail: permissionDetail(s),
            since: permissionSince(s),
        };
    }
    if (s.status === 'waiting_for_user') {
        return {
            ...base,
            key: `question:${s.id}`,
            kind: 'question',
            detail: s.pending_question?.question ?? '',
            since: ms(s.pending_question?.asked_at),
        };
    }
    if (s.status === 'error') {
        const msg = ctx.errors[s.id] ?? '';
        const gates = GATES_EXHAUSTED_RE.test(msg);
        return {
            ...base,
            key: `${gates ? 'gates' : 'error'}:${s.id}`,
            kind: gates ? 'gates_exhausted' : 'error',
            detail: msg,
            since: ms(s.last_activity),
        };
    }
    if (s.status === 'rate_limited') {
        return {
            ...base,
            key: `rate_limit:${s.id}`,
            kind: 'rate_limit',
            detail: '',
            since: ms(s.rate_limit_until),
        };
    }
    if (s.status === 'idle') {
        const stoppedAt = ms(s.stopped_after_task_at);
        if (stoppedAt !== null) {
            return { ...base, key: `stopped:${s.id}`, kind: 'stopped', detail: '', since: stoppedAt };
        }
        if (queueEmptied(s, ctx.queues[s.id])) {
            return { ...base, key: `queue_empty:${s.id}`, kind: 'queue_empty', detail: '', since: ms(s.last_activity) };
        }
    }
    return null;
}

/** One row per waiting session (plus one per needs-human mixed task), most urgent first. */
export function buildWaiting(sessions: SessionState[], ctx: WaitingContext): WaitingItem[] {
    const rows: WaitingItem[] = [];
    for (const s of sessions) {
        const row = sessionRow(s, ctx);
        if (row) rows.push(row);
    }
    for (const t of ctx.mixed) {
        rows.push({
            key: `needs_human:${t.ID}`,
            kind: 'needs_human',
            project: t.Project,
            sessionId: '',
            detail: [t.WorkerName, t.Error].filter(Boolean).join(': '),
            since: null,
            taskId: t.ID,
        });
    }
    return rows.sort((a, b) => {
        if (KIND_RANK[a.kind] !== KIND_RANK[b.kind]) return KIND_RANK[a.kind] - KIND_RANK[b.kind];
        const sa = a.since ?? Number.MAX_SAFE_INTEGER;
        const sb = b.since ?? Number.MAX_SAFE_INTEGER;
        if (sa !== sb) return sa - sb;
        return a.key < b.key ? -1 : a.key > b.key ? 1 : 0;
    });
}
