// State behind the "waiting for you" screen (WASTE-TASKS.md WS-08). The list is
// derived from stores that already exist (sessions, queues); this file only adds
// what they lack: the on/off flag, the last error text per session, and the
// mixed tasks that ended needs_human.
import { writable, derived, get } from 'svelte/store';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { GetMixedRounds } from '../../wailsjs/go/main/App';
import { sessionList } from './sessions';
import { queues } from './queues';
import { projects } from './projects';
import { buildWaiting, type HumanTask } from '../lib/waiting';

/** [optimization] waiting_screen. Off: the status bar shows no button. */
export const waitingScreenEnabled = writable<boolean>(false);

// Takes the Go-exported AppConfig (PascalCase) as GetConfig returns it.
export function applyWaitingFromConfig(cfg: any) {
    waitingScreenEnabled.set(cfg?.Optimization?.WaitingScreen === true);
}

/** Last `session:error` text by session id; dropped when the session leaves `error`. */
export const sessionErrors = writable<Record<string, string>>({});

/** Mixed tasks that ended needs_human, across projects. */
export const humanTasks = writable<HumanTask[]>([]);

export async function refreshHumanTasks(): Promise<void> {
    const found: HumanTask[] = [];
    for (const p of get(projects)) {
        try {
            const list = ((await GetMixedRounds(p.name)) as any[] | null) ?? [];
            for (const t of list) {
                if (t?.Status === 'needs_human') {
                    found.push({ ID: t.ID, Project: t.Project || p.name, WorkerName: t.WorkerName, Error: t.Error });
                }
            }
        } catch {
            // A project without mixed programming has nothing to report.
        }
    }
    // A task id is unique; a repeat would break the keyed list.
    humanTasks.set(found.filter((t, i) => found.findIndex((u) => u.ID === t.ID) === i));
}

export const waitingItems = derived(
    [sessionList, queues, sessionErrors, humanTasks, waitingScreenEnabled],
    ([$list, $queues, $errors, $mixed, $enabled]) =>
        $enabled ? buildWaiting($list, { queues: $queues, errors: $errors, mixed: $mixed }) : [],
);

let started = false;

/** Subscribes to the events the list needs. Idempotent; call once from App. */
export function initWaiting(): void {
    if (started) return;
    started = true;

    EventsOn('session:error', (evt: { id: string; message: string }) => {
        if (!evt?.id) return;
        sessionErrors.update((m) => ({ ...m, [evt.id]: evt.message ?? '' }));
    });
    EventsOn('session:status', (evt: { id: string; status: string }) => {
        if (!evt?.id || evt.status === 'error') return;
        sessionErrors.update((m) => {
            if (!(evt.id in m)) return m;
            const { [evt.id]: _gone, ...rest } = m;
            return rest;
        });
    });
    EventsOn('worker:done', () => {
        if (get(waitingScreenEnabled)) void refreshHumanTasks();
    });
}
