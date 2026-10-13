// Catalog of the settings that change how a session behaves, resolved against
// the effective config (GetConfig() already has project overlays applied).
// The "Features" popup (SessionFeatures.svelte) renders it so a user can see
// which opt-in features are actually on for a session — most new features
// ship disabled by default, and without this nothing in the UI says so.
//
// Titles and descriptions live in the i18n fragment 'sessionFeatures'
// under `sessionFeatures.<key>.title` / `.desc`; warnings under
// `sessionFeatures.warn.<code>`.

export type FeatureScope = 'session' | 'project' | 'global';

export type FeatureGroup = 'tasks' | 'context' | 'learning' | 'safety' | 'performance' | 'notifications';

export interface FeatureItem {
    key: string;           // i18n key suffix and stable test id
    tomlKey: string;       // the config.toml key that controls it
    scope: FeatureScope;   // where it is set
    group: FeatureGroup;
    enabled: boolean;
    value?: string;        // shown next to the state when it is more than on/off
    warning?: string;      // i18n key suffix under sessionFeatures.warn.*
    fields: FeatureField[]; // editable inputs; empty = change it in Settings
}

// Which config object a field lives in. 'session' and 'project' are the
// entries of this session/project inside cfg.Projects; the other two are
// cfg.Settings and cfg.Optimization (global).
export type FieldTarget = 'session' | 'project' | 'settings' | 'optimization';

export interface FeatureField {
    target: FieldTarget;
    prop: string;          // Go field name as serialized by Wails (PascalCase)
    label: string;         // the config.toml key, shown next to the input
    kind: 'bool' | 'number' | 'text' | 'textarea' | 'list' | 'select';
    options?: string[];    // for 'select'
    step?: number;         // for 'number'
}

const f = (target: FieldTarget, prop: string, label: string, kind: FeatureField['kind'],
           extra: Partial<FeatureField> = {}): FeatureField => ({ target, prop, label, kind, ...extra });

// Editable fields per feature key. A key missing here is read-only in the
// popup (mirror: toggled through MirrorProject/UnmirrorProject in Settings,
// which also run the first sync).
const FIELDS: Record<string, FeatureField[]> = {
    taskSource: [f('session', 'TaskSource', 'task_source', 'text')],
    autoRestart: [f('session', 'AutoRestart', 'auto_restart', 'bool'), f('session', 'MaxTasks', 'max_tasks', 'number')],
    stopWhenNoTasks: [f('session', 'StopWhenNoTasks', 'stop_when_no_tasks', 'bool')],
    useWorktree: [f('session', 'UseWorktree', 'use_worktree', 'bool'), f('session', 'WorktreeName', 'worktree_name', 'text')],
    preflight: [f('session', 'Preflight', 'preflight', 'select', { options: ['auto', 'always', 'never'] }),
                f('settings', 'PreflightAnalysis', 'preflight_analysis', 'bool')],
    taskHooks: [f('session', 'PreTaskHook', 'pre_task_hook', 'text'), f('session', 'PostTaskHook', 'post_task_hook', 'text')],
    crashRecovery: [f('settings', 'CrashRecovery', 'crash_recovery', 'bool')],
    contextRestart: [f('optimization', 'ContextRestartMode', 'context_restart_mode', 'select', { options: ['resume', 'fresh', 'ask', 'off'] }),
                     f('optimization', 'ContextRestartThreshold', 'context_restart_threshold', 'number', { step: 0.05 })],
    contextHandoff: [f('session', 'ContextHandoff', 'context_handoff', 'bool')],
    handoffContextTokens: [f('session', 'HandoffContextTokens', 'handoff_context_tokens', 'number', { step: 1000 })],
    contextPrimer: [f('session', 'ContextPrimer', 'context_primer', 'bool')],
    askUserMarker: [f('session', 'AskUserMarker', 'ask_user_marker', 'bool')],
    systemPromptAppend: [f('session', 'SystemPromptAppend', 'system_prompt_append', 'textarea')],
    addDirs: [f('session', 'AddDirs', 'add_dirs', 'list')],
    experienceTracking: [f('optimization', 'ExperienceTracking', 'experience_tracking', 'bool')],
    journal: [f('project', 'Journal', 'journal', 'bool')],
    journalCommit: [f('project', 'JournalCommit', 'journal_commit', 'bool')],
    autoSkills: [f('project', 'AutoSkills', 'auto_skills', 'bool'),
                 f('project', 'AutoSkillsDailyBudgetUSD', 'auto_skills_daily_budget_usd', 'number', { step: 0.5 })],
    mixedProgramming: [f('project', 'MixedProgramming', 'mixed_programming', 'bool'), f('project', 'Gates', 'gates', 'list')],
    permissionMode: [f('session', 'PermissionMode', 'permission_mode', 'select',
                       { options: ['bypassPermissions', 'acceptEdits', 'default', 'auto', 'plan', 'dontAsk'] })],
    permissionRules: [f('session', 'AllowedTools', 'allowed_tools', 'list'), f('session', 'DisallowedTools', 'disallowed_tools', 'list')],
    maxBudget: [f('session', 'MaxBudgetUSD', 'max_budget_usd', 'number', { step: 0.5 })],
    fallbackModel: [f('session', 'FallbackModelOnRateLimit', 'fallback_model_on_rate_limit', 'bool'),
                    f('session', 'FallbackModel', 'fallback_model', 'text')],
    loopDetection: [f('optimization', 'LoopDetection', 'loop_detection', 'bool'),
                    f('optimization', 'LoopAction', 'loop_action', 'select', { options: ['warn', 'send_hint', 'restart'] })],
    autoModelRouting: [f('optimization', 'AutoModelRouting', 'auto_model_routing', 'bool')],
    maxTurnsAutoAdjust: [f('optimization', 'MaxTurnsAutoAdjust', 'max_turns_auto_adjust', 'bool'),
                         f('optimization', 'MaxTurnsDefault', 'max_turns_default', 'number')],
    excludeDynamicSystemPrompt: [f('optimization', 'ExcludeDynamicSystemPrompt', 'exclude_dynamic_system_prompt', 'bool')],
    sessionStartDelay: [f('settings', 'SessionStartDelay', 'session_start_delay', 'number')],
    pythonUtf8: [f('settings', 'PythonUTF8', 'python_utf8', 'bool')],
    permissionNotify: [f('settings', 'PermissionNativeNotification', 'permission_native_notification', 'bool')],
    permissionSound: [f('settings', 'PermissionSound', 'permission_sound', 'bool')],
    permissionTimeout: [f('settings', 'PermissionTimeout', 'permission_timeout', 'number'),
                        f('settings', 'PermissionTimeoutAction', 'permission_timeout_action', 'select', { options: ['deny', 'pause_session'] })],
    budgetAlerts: [f('settings', 'DailyBudgetAlert', 'daily_budget_alert', 'number', { step: 1 }),
                   f('settings', 'WeeklyBudgetAlert', 'weekly_budget_alert', 'number', { step: 1 })],
};

/** fieldTarget returns the object inside cfg that a field reads and writes. */
export function fieldTarget(cfg: any, project: string, sessionName: string, target: FieldTarget): any {
    if (target === 'settings') return cfg?.Settings;
    if (target === 'optimization') return cfg?.Optimization;
    const p = (cfg?.Projects ?? []).find((x: any) => x?.Name === project);
    if (target === 'project') return p;
    return (p?.Sessions ?? []).find((x: any) => x?.Name === sessionName);
}

// Settings the backend reads from its live config at the moment of use, so
// a change applies at once — also to a session that is already running.
// Everything else is read when a session starts (CLI flags, the session's
// own config copy, start gates), so a running session needs a restart to
// pick it up; the popup offers one.
export const LIVE = new Set([
    'crashRecovery',
    'experienceTracking',
    'journal',
    'journalCommit',
    'autoSkills',
    'sessionStartDelay',
    'permissionNotify',
    'permissionSound',
    'permissionTimeout',
    'budgetAlerts',
]);

export const GROUP_ORDER: FeatureGroup[] = ['tasks', 'context', 'learning', 'safety', 'performance', 'notifications'];

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const num = (v: unknown): number => (typeof v === 'number' && isFinite(v) ? v : 0);
const list = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x) => typeof x === 'string' && x.trim() !== '') : []);

/**
 * describeSessionFeatures resolves every catalogued setting for one session.
 * Returns null when the project or session is not in the config (e.g. it was
 * removed while the session view was open).
 */
export function describeSessionFeatures(cfg: any, project: string, sessionName: string): FeatureItem[] | null {
    const p = (cfg?.Projects ?? []).find((x: any) => x?.Name === project);
    const s = (p?.Sessions ?? []).find((x: any) => x?.Name === sessionName);
    if (!p || !s) return null;
    const g = cfg?.Settings ?? {};
    const o = cfg?.Optimization ?? {};

    const items: FeatureItem[] = [];
    const add = (it: Omit<FeatureItem, 'fields'>) => items.push({ ...it, fields: FIELDS[it.key] ?? [] });

    // --- Tasks and lifecycle ---------------------------------------------
    const taskSource = str(s.TaskSource);
    add({ key: 'taskSource', tomlKey: 'task_source', scope: 'session', group: 'tasks',
        enabled: taskSource !== '', value: taskSource || undefined });
    add({ key: 'autoRestart', tomlKey: 'auto_restart', scope: 'session', group: 'tasks',
        enabled: !!s.AutoRestart,
        value: s.AutoRestart && num(s.MaxTasks) > 0 ? `max_tasks = ${num(s.MaxTasks)}` : undefined,
        warning: s.AutoRestart && taskSource === '' ? 'autoRestartNoQueue' : undefined });
    add({ key: 'stopWhenNoTasks', tomlKey: 'stop_when_no_tasks', scope: 'session', group: 'tasks',
        enabled: !!s.StopWhenNoTasks });
    add({ key: 'useWorktree', tomlKey: 'use_worktree', scope: 'session', group: 'tasks',
        enabled: !!s.UseWorktree, value: s.UseWorktree ? str(s.WorktreeName) || undefined : undefined });
    // Mirrors PreflightMode in internal/session/preflight_gate.go.
    const preflight = str(s.Preflight) || 'auto';
    const preflightSkipped = str(s.Prompt).trim() === '' || taskSource !== '' || !!s.AutoRestart;
    add({ key: 'preflight', tomlKey: 'preflight / preflight_analysis', scope: 'session', group: 'tasks',
        enabled: !!g.PreflightAnalysis && preflight !== 'never' && !preflightSkipped,
        value: preflight,
        warning: !!g.PreflightAnalysis && preflight !== 'never' && preflightSkipped ? 'preflightSkipped' : undefined });
    const hooks = [str(s.PreTaskHook) && 'pre', str(s.PostTaskHook) && 'post'].filter(Boolean).join(' + ');
    add({ key: 'taskHooks', tomlKey: 'pre_task_hook / post_task_hook', scope: 'session', group: 'tasks',
        enabled: hooks !== '', value: hooks || undefined });
    add({ key: 'crashRecovery', tomlKey: 'crash_recovery', scope: 'global', group: 'tasks',
        enabled: !!g.CrashRecovery });

    // --- Context -----------------------------------------------------------
    const restartMode = str(o.ContextRestartMode) || 'off';
    add({ key: 'contextRestart', tomlKey: 'context_restart_mode', scope: 'global', group: 'context',
        enabled: restartMode !== 'off',
        value: restartMode !== 'off' ? `${restartMode}, ${Math.round(num(o.ContextRestartThreshold) * 100)}%` : undefined });
    add({ key: 'contextHandoff', tomlKey: 'context_handoff', scope: 'session', group: 'context',
        enabled: !!s.ContextHandoff,
        warning: s.ContextHandoff && restartMode === 'off' ? 'handoffNoRestart' : undefined });
    const handoffTokens = num(s.HandoffContextTokens);
    add({ key: 'handoffContextTokens', tomlKey: 'handoff_context_tokens', scope: 'session', group: 'context',
        enabled: handoffTokens > 0, value: handoffTokens > 0 ? handoffTokens.toLocaleString('en-US') : undefined });
    add({ key: 'contextPrimer', tomlKey: 'context_primer', scope: 'session', group: 'context',
        enabled: !!s.ContextPrimer });
    add({ key: 'askUserMarker', tomlKey: 'ask_user_marker', scope: 'session', group: 'context',
        enabled: !!s.AskUserMarker });
    add({ key: 'systemPromptAppend', tomlKey: 'system_prompt_append', scope: 'session', group: 'context',
        enabled: str(s.SystemPromptAppend).trim() !== '' });
    const dirs = list(s.AddDirs);
    add({ key: 'addDirs', tomlKey: 'add_dirs', scope: 'session', group: 'context',
        enabled: dirs.length > 0, value: dirs.length > 0 ? dirs.join(', ') : undefined });

    // --- Learning / experience layer --------------------------------------
    add({ key: 'experienceTracking', tomlKey: 'experience_tracking', scope: 'global', group: 'learning',
        enabled: !!o.ExperienceTracking });
    add({ key: 'journal', tomlKey: 'journal', scope: 'project', group: 'learning',
        enabled: !!p.Journal });
    add({ key: 'journalCommit', tomlKey: 'journal_commit', scope: 'project', group: 'learning',
        enabled: !!p.JournalCommit,
        warning: p.JournalCommit && !p.Journal ? 'journalCommitNoJournal' : undefined });
    add({ key: 'autoSkills', tomlKey: 'auto_skills', scope: 'project', group: 'learning',
        enabled: !!p.AutoSkills,
        value: p.AutoSkills && num(p.AutoSkillsDailyBudgetUSD) > 0 ? `$${num(p.AutoSkillsDailyBudgetUSD)}/day` : undefined,
        warning: p.AutoSkills && !o.ExperienceTracking ? 'autoSkillsNoTracking' : undefined });
    const gates = list(p.Gates);
    add({ key: 'mixedProgramming', tomlKey: 'mixed_programming', scope: 'project', group: 'learning',
        enabled: !!p.MixedProgramming,
        value: p.MixedProgramming && gates.length > 0 ? `gates: ${gates.length}` : undefined,
        warning: p.MixedProgramming && gates.length === 0 ? 'mixedNoGates' : undefined });
    add({ key: 'mirror', tomlKey: 'mirror', scope: 'project', group: 'learning',
        enabled: !!p.Mirror });

    // --- Safety and budget -------------------------------------------------
    add({ key: 'permissionMode', tomlKey: 'permission_mode', scope: 'session', group: 'safety',
        enabled: str(s.PermissionMode) !== 'bypassPermissions', value: str(s.PermissionMode) || undefined });
    const ruleCount = list(s.AllowedTools).length + list(s.DisallowedTools).length + (s.PermissionRules?.length ?? 0);
    add({ key: 'permissionRules', tomlKey: 'allowed_tools / disallowed_tools / permission_rule', scope: 'session', group: 'safety',
        enabled: ruleCount > 0, value: ruleCount > 0 ? String(ruleCount) : undefined });
    const budget = num(s.MaxBudgetUSD);
    add({ key: 'maxBudget', tomlKey: 'max_budget_usd', scope: 'session', group: 'safety',
        enabled: budget > 0, value: budget > 0 ? `$${budget}` : undefined });
    add({ key: 'fallbackModel', tomlKey: 'fallback_model_on_rate_limit', scope: 'session', group: 'safety',
        enabled: !!s.FallbackModelOnRateLimit && str(s.FallbackModel) !== '',
        value: str(s.FallbackModel) || undefined,
        warning: s.FallbackModelOnRateLimit && str(s.FallbackModel) === '' ? 'fallbackNoModel' : undefined });
    const loopOn = !!o.LoopDetection;
    add({ key: 'loopDetection', tomlKey: 'loop_detection', scope: 'global', group: 'safety',
        enabled: loopOn, value: loopOn ? str(o.LoopAction) || undefined : undefined });

    // --- Performance and cost ---------------------------------------------
    add({ key: 'autoModelRouting', tomlKey: 'auto_model_routing', scope: 'global', group: 'performance',
        enabled: !!o.AutoModelRouting });
    add({ key: 'maxTurnsAutoAdjust', tomlKey: 'max_turns_auto_adjust', scope: 'global', group: 'performance',
        enabled: !!o.MaxTurnsAutoAdjust });
    add({ key: 'excludeDynamicSystemPrompt', tomlKey: 'exclude_dynamic_system_prompt', scope: 'global', group: 'performance',
        enabled: !!o.ExcludeDynamicSystemPrompt });
    const startDelay = num(g.SessionStartDelay) || num(o.SessionStartDelay);
    add({ key: 'sessionStartDelay', tomlKey: 'session_start_delay', scope: 'global', group: 'performance',
        enabled: startDelay > 0, value: startDelay > 0 ? `${startDelay}s` : undefined });
    add({ key: 'pythonUtf8', tomlKey: 'python_utf8', scope: 'global', group: 'performance',
        enabled: !!g.PythonUTF8 });

    // --- Notifications -----------------------------------------------------
    add({ key: 'permissionNotify', tomlKey: 'permission_native_notification', scope: 'global', group: 'notifications',
        enabled: !!g.PermissionNativeNotification });
    add({ key: 'permissionSound', tomlKey: 'permission_sound', scope: 'global', group: 'notifications',
        enabled: !!g.PermissionSound });
    const permTimeout = num(g.PermissionTimeout);
    add({ key: 'permissionTimeout', tomlKey: 'permission_timeout', scope: 'global', group: 'notifications',
        enabled: permTimeout > 0,
        value: permTimeout > 0 ? `${permTimeout}s → ${str(g.PermissionTimeoutAction) || 'deny'}` : undefined });
    const daily = num(g.DailyBudgetAlert), weekly = num(g.WeeklyBudgetAlert);
    add({ key: 'budgetAlerts', tomlKey: 'daily_budget_alert / weekly_budget_alert', scope: 'global', group: 'notifications',
        enabled: daily > 0 || weekly > 0,
        value: daily > 0 || weekly > 0 ? [daily > 0 && `$${daily}/day`, weekly > 0 && `$${weekly}/week`].filter(Boolean).join(', ') : undefined });

    return items;
}
