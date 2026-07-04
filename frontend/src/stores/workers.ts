import { writable, get } from 'svelte/store';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import {
    DispatchMixedTask,
    RegisterMixedBrief,
    GetMixedRounds,
    GetMixedQuality,
    CancelMixedTask,
} from '../../wailsjs/go/main/App';

// ---- Types mirroring Go worker structs (internal/worker) ----

export interface Patch {
    Index: number;
    File: string;
    Find: string;
    Replace: string;
}

export interface RejectedPatch {
    Patch: Patch;
    Reason: string;
}

export interface GateCommandResult {
    Command: string;
    Output: string;
    ExitCode: number;
    Duration: number;
    Err?: unknown;
}

export interface GateResult {
    Commands: GateCommandResult[] | null;
    Passed: boolean;
}

export interface RoundRecord {
    Number: number;
    RawOutput: string;
    ParseError: string;
    ParseWarnings: string[] | null;
    Applied: Patch[] | null;
    Rejected: RejectedPatch[] | null;
    Gates: GateResult;
    Passed: boolean;
    FeedbackSent: string;
}

export type MixedTaskStatus = 'running' | 'done' | 'needs_human';

export interface MixedTask {
    ID: string;
    Project: string;
    BriefID: string;
    WorkerName: string;
    Branch: string;
    WorktreePath: string;
    Status: MixedTaskStatus;
    MaxRounds: number;
    Rounds: RoundRecord[] | null;
    Error: string;
}

export interface ModelQuality {
    worker: string;
    tasks_total: number;
    tasks_done: number;
    tasks_needs_human: number;
    tasks_running: number;
    avg_rounds_to_green: number;
    patches_applied: number;
    patches_rejected: number;
    clean_patch_rate: number;
    parse_errors: number;
    gate_failures: number;
}

// A single line in the live activity feed, derived from worker:* events. This
// gives immediate feedback while a dispatch is in flight, before the persisted
// task state (loaded via GetMixedRounds) catches up.
export interface WorkerActivity {
    task_id: string;
    kind: 'round' | 'patch' | 'gate' | 'done';
    round?: number;
    message: string;
}

// ---- Stores ----

// Persisted mixed tasks for the currently viewed project.
export const mixedTasks = writable<MixedTask[]>([]);

// Comparative quality report for the currently viewed project.
export const mixedQuality = writable<ModelQuality[]>([]);

// Live activity feed (most recent last), capped to keep memory bounded.
export const workerActivity = writable<WorkerActivity[]>([]);

// True while a DispatchMixedTask call is awaiting its terminal result.
export const dispatching = writable<boolean>(false);

// The project the store currently reflects — used to filter live events.
const activeProject = writable<string>('');

const ACTIVITY_LIMIT = 500;

function pushActivity(a: WorkerActivity) {
    workerActivity.update((list) => {
        const next = [...list, a];
        return next.length > ACTIVITY_LIMIT ? next.slice(next.length - ACTIVITY_LIMIT) : next;
    });
}

// ---- Actions ----

/** Load persisted tasks + quality for a project and mark it as the active view. */
export async function loadProject(project: string): Promise<void> {
    activeProject.set(project);
    await Promise.all([refreshTasks(project), refreshQuality(project)]);
}

export async function refreshTasks(project: string): Promise<void> {
    try {
        const list = (await GetMixedRounds(project)) as MixedTask[] | null;
        mixedTasks.set(list ?? []);
    } catch (e) {
        console.warn('GetMixedRounds failed:', e);
        mixedTasks.set([]);
    }
}

export async function refreshQuality(project: string): Promise<void> {
    try {
        const list = (await GetMixedQuality(project)) as ModelQuality[] | null;
        mixedQuality.set(list ?? []);
    } catch (e) {
        console.warn('GetMixedQuality failed:', e);
        mixedQuality.set([]);
    }
}

/**
 * Register a brief and dispatch it against a worker, then refresh persisted
 * state. Resolves with the terminal MixedTask. DispatchMixedTask blocks on the
 * backend until the task reaches done/needs_human, so the live event feed is
 * what shows progress meanwhile.
 */
export async function dispatch(
    project: string,
    briefID: string,
    task: string,
    workerName: string,
): Promise<MixedTask> {
    dispatching.set(true);
    try {
        await RegisterMixedBrief(briefID, task, '');
        const result = (await DispatchMixedTask(project, briefID, workerName)) as MixedTask;
        await Promise.all([refreshTasks(project), refreshQuality(project)]);
        return result;
    } finally {
        dispatching.set(false);
    }
}

/** Cancel a running task by ID. */
export async function cancel(taskID: string): Promise<void> {
    await CancelMixedTask(taskID);
}

export function clearActivity(): void {
    workerActivity.set([]);
}

// ---- Event subscriptions ----

let initialized = false;

export function initWorkers(): void {
    if (initialized) return;
    initialized = true;

    const belongsToActiveView = (taskID: string): boolean => {
        const proj = get(activeProject);
        // Task IDs are "<project>/<brief>/<worker>"; match on the project prefix.
        return proj !== '' && taskID.startsWith(proj + '/');
    };

    EventsOn('worker:round', (evt: { task_id: string; round?: number; event: string; branch?: string }) => {
        if (!evt?.task_id || !belongsToActiveView(evt.task_id)) return;
        const msg = evt.event === 'worktree_created'
            ? `worktree created (${evt.branch ?? ''})`
            : `round ${evt.round ?? '?'} started`;
        pushActivity({ task_id: evt.task_id, kind: 'round', round: evt.round, message: msg });
    });

    EventsOn('worker:patch', (evt: { task_id: string; round: number; applied: number; rejected: number; error?: string }) => {
        if (!evt?.task_id || !belongsToActiveView(evt.task_id)) return;
        const msg = evt.error
            ? `round ${evt.round}: parse error — ${evt.error}`
            : `round ${evt.round}: ${evt.applied} applied, ${evt.rejected} rejected`;
        pushActivity({ task_id: evt.task_id, kind: 'patch', round: evt.round, message: msg });
    });

    EventsOn('worker:gate', (evt: { task_id: string; round: number; passed: boolean }) => {
        if (!evt?.task_id || !belongsToActiveView(evt.task_id)) return;
        pushActivity({
            task_id: evt.task_id,
            kind: 'gate',
            round: evt.round,
            message: `round ${evt.round}: gates ${evt.passed ? 'passed' : 'failed'}`,
        });
    });

    EventsOn('worker:done', (evt: { task_id: string; status: string; rounds: number }) => {
        if (!evt?.task_id || !belongsToActiveView(evt.task_id)) return;
        pushActivity({
            task_id: evt.task_id,
            kind: 'done',
            message: `finished: ${evt.status} after ${evt.rounds} round(s)`,
        });
        // Persisted state changed — refresh the view.
        const proj = get(activeProject);
        if (proj) {
            void refreshTasks(proj);
            void refreshQuality(proj);
        }
    });
}
