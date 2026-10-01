// Per-session task queue snapshots (the slice of the roadmap a session's
// status file points at), shared by the sidebar mini-bar, the TaskPanel Queue
// tab and the board so one GetSessionRoadmap call serves all three.
import { writable } from 'svelte/store';
import { GetSessionRoadmap } from '../../wailsjs/go/main/App';
import type { RoadmapView } from '../components/roadmap';
import { buildQueue, EMPTY_QUEUE, type QueueModel } from '../lib/queue';

export type QueueSnapshot = {
    model: QueueModel;
    loading: boolean;
    error: string;
};

type Syncable = {
    id: string;
    project: string;
    name: string;
    task_source_description?: string;
    tasks_done?: number;
};

export const queues = writable<Record<string, QueueSnapshot>>({});

const keys = new Map<string, string>();
const seq = new Map<string, number>();

function patch(id: string, p: Partial<QueueSnapshot>) {
    queues.update((m) => ({
        ...m,
        [id]: { model: EMPTY_QUEUE, loading: false, error: '', ...m[id], ...p },
    }));
}

/**
 * Re-reads the queue when the backend reports a new task description or a
 * finished task — both mean a pointer line was consumed. Idempotent: callers
 * invoke it from reactive statements.
 */
export function syncQueue(s: Syncable) {
    const key = `${s.task_source_description ?? ''}/${s.tasks_done ?? 0}`;
    if (keys.get(s.id) === key) return;
    keys.set(s.id, key);
    void loadQueue(s);
}

export async function loadQueue(s: Syncable) {
    const n = (seq.get(s.id) ?? 0) + 1;
    seq.set(s.id, n);
    patch(s.id, { loading: true });
    try {
        const view = ((await GetSessionRoadmap(s.project, s.name)) as RoadmapView | null) ?? null;
        if (seq.get(s.id) !== n) return; // a newer load superseded this one
        patch(s.id, { model: buildQueue(view), loading: false, error: '' });
    } catch (e: any) {
        if (seq.get(s.id) !== n) return;
        patch(s.id, { loading: false, error: e?.message ?? String(e) });
    }
}
