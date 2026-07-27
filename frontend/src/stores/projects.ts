import { writable, derived } from 'svelte/store';
import { GetProjects } from '../../wailsjs/go/main/App';
import { sessions, type SessionState } from './sessions';

export interface SessionConfigSummary {
    name: string;
    prompt?: string;
    model?: string;
    permission_mode?: string;
}

export interface ProjectInfo {
    name: string;
    path: string;
    sessions: SessionConfigSummary[];
}

// All configured projects (from TOML, loaded via Wails).
export const projects = writable<ProjectInfo[]>([]);

// UI state: which project trees are collapsed (default: all expanded).
export const collapsedProjects = writable<Record<string, boolean>>({});

export function toggleProject(name: string) {
    collapsedProjects.update((m) => ({ ...m, [name]: !m[name] }));
}

// Combined view: project + its sessions grouped by project name.
// A session row exists either if it's running (in `sessions` store) or
// configured (in `projects`) — configured-but-not-running sessions
// appear as Idle placeholders.
export interface ProjectGroup {
    name: string;
    path: string;
    sessions: SessionState[];
}

export const projectGroups = derived(
    [projects, sessions],
    ([$projects, $sessions]) => {
        const runningByProject = new Map<string, Map<string, SessionState>>();
        Object.values($sessions).forEach((s) => {
            if (!runningByProject.has(s.project)) {
                runningByProject.set(s.project, new Map());
            }
            runningByProject.get(s.project)!.set(s.name, s);
        });

        const groups: ProjectGroup[] = $projects.map((p) => {
            const running = runningByProject.get(p.name) ?? new Map();
            const merged: SessionState[] = p.sessions.map((sc) => {
                const live = running.get(sc.name);
                if (live) return live;
                return placeholder(p.name, sc.name);
            });

            // Any running sessions not in config (e.g. ad-hoc) — append.
            running.forEach((s, name) => {
                if (!p.sessions.find((sc) => sc.name === name)) {
                    merged.push(s);
                }
            });

            return { name: p.name, path: p.path, sessions: merged };
        });

        // Any project that has running sessions but isn't in config:
        runningByProject.forEach((map, projName) => {
            if (!$projects.find((p) => p.name === projName)) {
                groups.push({
                    name: projName,
                    path: '',
                    sessions: Array.from(map.values()),
                });
            }
        });

        return groups;
    },
);

function placeholder(project: string, name: string): SessionState {
    return {
        id: `${project}/${name}`,
        project,
        name,
        status: 'idle',
        model: '',
        effort: '',
        permission_mode: '',
        started_at: '',
        last_activity: '',
        rate_limit_until: '',
        tasks_done: 0,
        current_task: '',
        task_source_description: '',
        prompt: '',
        todos: [],
        branch: '',
        cli_session_id: '',
        stop_requested: false,
        pending_permission: null,
        input_tokens: 0,
        output_tokens: 0,
        cache_read: 0,
        cache_creation: 0,
        num_turns: 0,
        total_cost_usd: 0,
        context_window: 0,
        context_util: 0,
    };
}

export async function initProjects(): Promise<void> {
    try {
        const list = (await GetProjects()) as any[];
        const mapped: ProjectInfo[] = (list ?? []).map((p) => ({
            name: p.Name ?? p.name ?? '',
            path: p.Path ?? p.path ?? '',
            sessions: ((p.Sessions ?? p.sessions ?? []) as any[]).map((s) => ({
                name: s.Name ?? s.name ?? '',
                prompt: s.Prompt ?? s.prompt,
                model: s.Model ?? s.model,
                permission_mode: s.PermissionMode ?? s.permission_mode,
            })),
        }));
        projects.set(mapped);
    } catch (e) {
        console.warn('GetProjects failed:', e);
    }
}
