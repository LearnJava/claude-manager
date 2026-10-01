// Pure model of a session's task queue, derived from the roadmap tree that
// GetSessionRoadmap returns (internal/analysis/roadmapview.go). The tree is the
// whole backlog; the queue is the slice of it the session's status file points
// at (`in_queue`, first pointer = `current`). Three views render this model:
// the TaskPanel "Queue" tab, the sidebar mini-bar and the board.
import type { RoadmapNode, RoadmapView } from '../components/roadmap';

export type QueueItem = {
    /** Row id as written in the roadmap (RT-04, 12, …); '#N' for generated roadmaps. */
    id: string;
    name: string;
    summary: string;
    /** Upper-cased size cell (S/M/L…), '' when the roadmap has none. */
    size: string;
    status: RoadmapNode['status'];
    current: boolean;
    /** Ids this row depends on, as written. */
    dependsOn: string[];
    /** The subset of dependsOn that is itself still queued (not done). */
    waitsFor: string[];
};

export type QueueModel = {
    current: QueueItem | null;
    /** Queued rows after the current one, in roadmap order. */
    upcoming: QueueItem[];
    /** Finished leaf tasks, newest (last in the roadmap) first, capped. */
    doneRecent: QueueItem[];
    doneCount: number;
    total: number;
};

export const EMPTY_QUEUE: QueueModel = {
    current: null,
    upcoming: [],
    doneRecent: [],
    doneCount: 0,
    total: 0,
};

/** How many finished tasks the model keeps for display. */
export const DONE_RECENT_LIMIT = 8;

export function hasQueue(m: QueueModel): boolean {
    return m.current !== null || m.upcoming.length > 0;
}

/** Count of rows still ahead, current included. */
export function queuedCount(m: QueueModel): number {
    return (m.current ? 1 : 0) + m.upcoming.length;
}

function nodeId(n: RoadmapNode): string {
    if (n.id) return n.id;
    return n.number > 0 ? `#${n.number}` : '';
}

/** Splits a `depends_on` cell ("RT-04, RT-05", "-", "#3") into row ids. */
export function parseDeps(cell: string): string[] {
    return (cell ?? '')
        .split(/[\s,;]+/)
        .map((s) => s.trim())
        .filter((s) => s !== '' && s !== '-' && s !== '—');
}

function norm(id: string): string {
    return id.replace(/^#/, '').toLowerCase();
}

function toItem(n: RoadmapNode): QueueItem {
    return {
        id: nodeId(n),
        name: n.name || n.summary,
        summary: n.name ? n.summary : '',
        size: (n.size ?? '').trim().toUpperCase(),
        status: n.status,
        current: n.current,
        dependsOn: parseDeps(n.depends_on),
        waitsFor: [],
    };
}

/** Builds the queue model from a roadmap view; null/empty view gives EMPTY_QUEUE. */
export function buildQueue(view: RoadmapView | null | undefined): QueueModel {
    if (!view) return EMPTY_QUEUE;

    const queued: QueueItem[] = [];
    const done: QueueItem[] = [];
    const walk = (nodes: RoadmapNode[]) => {
        for (const n of nodes) {
            if (n.kind === 'task') {
                if (n.in_queue && n.status !== 'done') queued.push(toItem(n));
                else if (n.status === 'done' && (n.children ?? []).length === 0) done.push(toItem(n));
            }
            if (n.children?.length) walk(n.children);
        }
    };
    walk(view.nodes ?? []);

    // Pointer order is not exposed, only which row is first: keep roadmap
    // order for the rest and put the current row on top.
    const ci = queued.findIndex((q) => q.current);
    const current = ci >= 0 ? queued.splice(ci, 1)[0] : null;

    const open = new Set(
        [...(current ? [current] : []), ...queued].filter((q) => q.id).map((q) => norm(q.id)),
    );
    for (const q of [...(current ? [current] : []), ...queued]) {
        q.waitsFor = q.dependsOn.filter((d) => norm(d) !== norm(q.id) && open.has(norm(d)));
    }

    return {
        current,
        upcoming: queued,
        doneRecent: done.slice(-DONE_RECENT_LIMIT).reverse(),
        doneCount: view.done ?? done.length,
        total: view.total ?? 0,
    };
}

export type Segment = 'done' | 'current' | 'queued';

/** Cap per kind so a 300-row backlog doesn't turn the strip into dust. */
export const SEGMENT_LIMIT = 12;

/**
 * Segments for the sidebar mini-bar: what this run finished (tasksDone), the
 * current row, and what is still queued. Capped; the numbers next to the bar
 * stay exact.
 */
export function queueSegments(m: QueueModel, tasksDone: number): Segment[] {
    const out: Segment[] = [];
    for (let i = 0; i < Math.min(Math.max(tasksDone, 0), SEGMENT_LIMIT); i++) out.push('done');
    if (m.current) out.push('current');
    for (let i = 0; i < Math.min(m.upcoming.length, SEGMENT_LIMIT); i++) out.push('queued');
    return out;
}

/** Tailwind classes for a size chip; unknown sizes stay neutral. */
export function sizeChipClass(size: string): string {
    switch (size[0]) {
        case 'S': return 'text-blue-600 dark:text-blue-400';
        case 'M': return 'text-yellow-700 dark:text-yellow-400';
        case 'L':
        case 'X': return 'text-orange-600 dark:text-orange-400';
        default: return 'text-text-muted';
    }
}
