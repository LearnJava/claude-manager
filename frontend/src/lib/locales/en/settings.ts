// Translation fragment: settings (en) — Settings.svelte.
export default {
    'settings.language': 'Language',

    // Header / tabs
    'settings.title': 'Settings',
    'settings.tab.global': 'Global',
    'settings.tab.projects': 'Projects',
    'settings.tab.sessions': 'Sessions',
    'settings.tab.workers': 'Workers',

    // Loading / empty states
    'settings.loadingConfig': 'Loading config…',
    'settings.noConfigLoaded': 'No config loaded.',

    // Shared field labels
    'settings.field.name': 'Name',
    'settings.field.model': 'Model',
    'settings.common.remove': 'Remove',

    // Global tab — General
    'settings.general.heading': 'General',
    'settings.general.claudePath': 'Claude CLI path',
    'settings.general.theme': 'Theme',
    'settings.general.retryDelay': 'Retry delay (sec)',
    'settings.general.rateLimitPause': 'Rate limit pause (sec)',
    'settings.general.logRetention': 'Log retention (days)',
    'settings.general.sessionStartDelay': 'Session start delay (sec)',
    'settings.general.sessionStartDelayTitle': 'Cache warming: stagger session starts by this many seconds.',

    // Global tab — Crash recovery
    'settings.crashRecoveryGlobal.heading': 'Crash recovery',
    'settings.crashRecoveryGlobal.enable': 'Enable crash recovery',
    'settings.crashRecoveryGlobal.desc1': 'When enabled, the manager saves a session state file to',
    'settings.crashRecoveryGlobal.desc2': 'before each task. If the app is closed or crashes while a session is running, the next start will resume the interrupted conversation via',
    'settings.crashRecoveryGlobal.useForceNew1': 'Use',
    'settings.crashRecoveryGlobal.forceNew': 'Force new',
    'settings.crashRecoveryGlobal.useForceNew2': 'per session (in Sessions tab) to discard saved state and start fresh.',

    // Global tab — Pre-flight analysis
    'settings.preflight.heading': 'Pre-flight analysis',
    'settings.preflight.enable': 'Enable pre-flight analysis',
    'settings.preflight.autoApproveSingle': 'Auto-approve single-session plans',
    'settings.preflight.analystModel': 'Analyst model',
    'settings.preflight.maxBudget': 'Analyst max budget USD (0 = none)',

    // Global tab — Permission timeouts
    'settings.permTimeouts.heading': 'Permission timeouts',
    'settings.permTimeouts.notifyAfter': 'Notify after (sec)',
    'settings.permTimeouts.timeout': 'Timeout (sec, 0 = wait forever)',
    'settings.permTimeouts.timeoutAction': 'Timeout action',
    'settings.permTimeouts.nativeNotification': 'Native notification (Windows toast)',
    'settings.permTimeouts.soundAlert': 'Sound alert',

    // Global tab — Model routing
    'settings.modelRouting.heading': 'Model routing',
    'settings.modelRouting.autoRouting': 'Auto model routing (choose model by task complexity via pre-flight)',

    // Global tab — Experience layer
    'settings.experienceLayer.heading': 'Experience layer',
    'settings.experienceLayer.enable': 'Index finished runs into the Actions tab (LEARN-TASKS.md LN-03)',
    'settings.experienceLayer.description': "Mines this app's own CLI transcripts into normalized tool-call signatures — no external service, nothing leaves this machine. Off by default: with it off, no transcript is ever opened.",

    // Global tab — Budget alerts
    'settings.budgetAlerts.heading': 'Budget alerts',
    'settings.budgetAlerts.daily': 'Daily alert (USD, 0 = off)',
    'settings.budgetAlerts.weekly': 'Weekly alert (USD, 0 = off)',
    'settings.budgetAlerts.rateLimitThreshold': 'Rate-limit alert threshold',

    // Projects tab
    'settings.projects.countSingular': '{count} project configured',
    'settings.projects.countPlural': '{count} projects configured',
    'settings.projects.addProjectButton': '+ Add project',
    'settings.projects.emptyPart1': 'No projects yet. Click',
    'settings.projects.addProjectLabel': 'Add project',
    'settings.projects.emptyPart2': 'to create one.',
    'settings.projects.projectNumber': 'Project #{n}',
    'settings.projects.path': 'Path',
    'settings.projects.browse': 'Browse…',
    'settings.projects.storageNote1': '📁 Sessions & gates are saved in',
    'settings.projects.storageNote2': '(commit it to share project context). The mixed-programming opt-in goes to',
    'settings.projects.storageNote3': '(gitignored, never committed).',
    'settings.projects.storageNoteNoPath': 'Without a project folder, settings stay in the global config.',
    'settings.projects.defaultPermMode': 'Default permission mode for new sessions',
    'settings.projects.inheritOption': '(inherit → bypassPermissions)',
    'settings.projects.defaultPermModeHint': 'Only seeds new sessions added to this project from now on — existing sessions keep their own saved value (editable in the Sessions tab).',
    'settings.projects.sessionCountSingular': '{count} session',
    'settings.projects.sessionCountPlural': '{count} sessions',
    'settings.projects.addSessionArrow': '+ Add session →',

    // Projects tab — Mixed programming
    'settings.mixed.enable': 'Enable mixed programming (external workers)',
    'settings.mixed.warning1': '⚠ Briefs and verbatim code excerpts are sent to external free endpoints that log requests. Only enable for projects whose code may leave your machine.',
    'settings.mixed.warning2a': 'This opt-in is stored in',
    'settings.mixed.warning2b': 'and is not committed — each teammate opts in for themselves.',
    'settings.mixed.gatesLabel': 'Gates (one command per line — blocking; a non-zero exit rejects the round)',
    'settings.mixed.maxRounds': 'Max feedback rounds',

    // Projects tab — AI roadmap generation
    'settings.roadmap.intro1': '🤖 Describe the project and let AI draft a roadmap: it decomposes the idea into a backlog of session-sized tasks, writes',
    'settings.roadmap.intro2': 'into the project, and configures a "P1" Sonnet session to work through them one at a time.',
    'settings.roadmap.foundDraft': 'Found an unreviewed roadmap from a previous run — {count} tasks, already generated (nothing spent re-running it).',
    'settings.roadmap.reviewButton': 'Review',
    'settings.roadmap.dismissButton': 'Dismiss',
    'settings.roadmap.ideaLabel': 'Project idea',
    'settings.roadmap.ideaPlaceholder': 'What do you want to build?',
    'settings.roadmap.recommendedSuffix': ' (recommended)',
    'settings.roadmap.generatingButton': 'Generating… {elapsed}',
    'settings.roadmap.generateButton': 'Generate Roadmap with AI',
    'settings.roadmap.startingAnalyst': 'Starting analyst session…',
    'settings.roadmap.setPathFirst': 'Set a project folder above first.',

    // Projects tab — Saved session logs
    'settings.logs.setPathFirst': 'Saved logs: set a project folder above first.',
    'settings.logs.countSingular': 'Saved logs: {count} file, {size}',
    'settings.logs.countPlural': 'Saved logs: {count} files, {size}',
    'settings.logs.unknown': 'Saved logs: —',
    'settings.logs.refreshTitle': 'Refresh saved-log count/size',
    'settings.logs.confirmDeleteTitle': 'Click again to confirm deletion',
    'settings.logs.deleteTitle': 'Delete every saved log file for this project',
    'settings.logs.confirmClearButton': 'Confirm clear?',
    'settings.logs.clearButton': '🗑 Clear project logs',
    'settings.logs.description': 'Every finished task/run auto-saves its log as markdown here. Clearing removes those files and their SQLite log entries — History/Dashboard run records are kept.',

    // Projects tab — Session protocol
    'settings.protocol.label': 'Session protocol',
    'settings.protocol.installTitle': 'Write docs/git-workflow.md, scripts/worktree-pool.sh and the /cm-task-start, /cm-task-finish skills into this project. Existing files are never overwritten.',
    'settings.protocol.installButton': 'Install session protocol',
    'settings.protocol.description1': 'Rules a queue-driven session follows: reserve a task with a branch, work in a persistent worktree slot, merge',
    'settings.protocol.description2': 'after every commit. Without them an interrupted session silently starts its task over. Roadmap-generated projects get this automatically.',

    // Script-generated status/error messages (Projects tab actions)
    'settings.msg.selectProjectFolder': 'Select project folder',
    'settings.msg.folderPickerFailed': 'Folder picker failed: {error}',
    'settings.msg.roadmapGenFailed': 'Roadmap generation failed: {error}',
    'settings.msg.roadmapWritten': 'ROADMAP.md and STATUS-P1.md written — the "P1" session is configured.',
    'settings.msg.installedFiles': 'Installed: {files}',
    'settings.msg.alreadyPresent': 'Already present — nothing written.',
    'settings.msg.protocolChecked': 'Session protocol checked for {project}.',
    'settings.msg.installProtocolFailed': 'Install protocol failed: {error}',
    'settings.msg.clearedLogs': 'Cleared saved logs for {project}.',
    'settings.msg.clearLogsFailed': 'Clear logs failed: {error}',
    'settings.msg.loadFailed': 'Load failed: {error}',
    'settings.msg.saved': 'Saved.',
    'settings.msg.saveFailed': 'Save failed: {error}',

    // Sessions tab
    'settings.sessions.addProjectFirst': 'Add a project first (Projects tab).',
    'settings.sessions.projectLabel': 'Project',
    'settings.sessions.projectFallback': '(project {n})',
    'settings.sessions.listHeading': 'Sessions',
    'settings.sessions.addButton': '+ Add',
    'settings.sessions.noSessions': 'No sessions.',
    'settings.sessions.sessionFallback': '(session {n})',
    'settings.sessions.selectOrAdd': 'Select or add a session.',

    'settings.sessions.identityHeading': 'Identity',
    'settings.sessions.taskSourceFile': 'Task source file',
    'settings.sessions.promptLabel': 'Prompt',

    'settings.sessions.modelHeading': 'Model',
    'settings.sessions.effort': 'Effort',
    'settings.sessions.fallbackModel': 'Fallback model',
    'settings.sessions.fallbackModelTitle': 'Model used when the primary model is rate-limited or overloaded.',
    'settings.sessions.fallbackToggleTitle': 'When rate-limited: switch to fallback_model immediately and restart without waiting. Mirrors orchestrator.py fallback behaviour.',
    'settings.sessions.fallbackToggleLabel': 'Switch to fallback model on rate limit (no wait)',

    'settings.sessions.permissionsHeading': 'Permissions',
    'settings.sessions.permissionMode': 'Permission mode',
    'settings.sessions.allowedTools': 'Allowed tools (one per line)',
    'settings.sessions.disallowedTools': 'Disallowed tools (one per line)',
    'settings.sessions.autoApproveRules': 'Auto-approve rules',
    'settings.sessions.addRule': '+ Add rule',
    'settings.sessions.noRules': 'No rules.',

    'settings.sessions.lifecycleHeading': 'Lifecycle',
    'settings.sessions.maxBudget': 'Max budget USD (0 = unlimited)',
    'settings.sessions.maxTasks': 'Max tasks (0 = unlimited)',
    'settings.sessions.autoRestart': 'Auto-restart after task',
    'settings.sessions.stopWhenNoTasks': 'Stop when no tasks',
    'settings.sessions.useWorktree': 'Use git worktree',
    'settings.sessions.preflightLabel': 'Pre-flight',

    'settings.sessions.hooksHeading': 'Hooks',
    'settings.sessions.preTaskHook': 'Pre-task hook',
    'settings.sessions.postTaskHook': 'Post-task hook',

    'settings.sessions.crashRecoveryHeading': 'Crash recovery',
    'settings.sessions.recoveryPrompt': 'Recovery prompt',
    'settings.sessions.recoveryPromptPlaceholder': '(default: check git status and continue the interrupted task)',
    'settings.sessions.recoveryPromptTitle': 'Message sent to Claude when resuming an interrupted session. Leave empty to use the built-in default.',

    'settings.sessions.contextHeading': 'Context',
    'settings.sessions.appendSystemPrompt': 'Append to system prompt',
    'settings.sessions.additionalDirs': 'Additional dirs (one per line)',

    // Workers tab
    'settings.workers.listHeading': 'Workers',
    'settings.workers.addButton': '+ Add',
    'settings.workers.noWorkers': 'No workers.',
    'settings.workers.fallback': '(worker {n})',
    'settings.workers.addFromPreset': 'Add from preset',
    'settings.workers.selectOrAdd': 'Select or add a worker.',
    'settings.workers.role': 'Role',
    'settings.workers.baseUrl': 'Base URL (OpenAI-compatible gateway)',
    'settings.workers.modelId': 'Model id',
    'settings.workers.apiKeyEnv': 'API key env var',
    'settings.workers.apiKeyEnvTitle': 'The API key is read from this environment variable — never stored in config.',
    'settings.workers.reasoningEffort': 'Reasoning effort',
    'settings.workers.maxOutputTokens': 'Max output tokens',
    'settings.workers.continuationCap': 'Continuation cap',
    'settings.workers.continuationCapTitle': 'Max finish_reason=length continuations before giving up (loop guard).',
    'settings.workers.requestTimeout': 'Request timeout (sec)',
    'settings.workers.asciiAnchorsTitle': 'Warn when generating a brief that its FIND anchors must be pure ASCII (Cyrillic anchors break some models).',
    'settings.workers.asciiAnchorsLabel': 'ASCII-only FIND anchors',

    // Footer
    'settings.footer.changesNote': 'Changes are written to TOML on Save.',
    'settings.footer.saving': 'Saving…',
} as Record<string, string>;
