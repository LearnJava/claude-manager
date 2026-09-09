// Translation fragment: resumePrompt (en). Keys are namespaced 'resumePrompt.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'resumePrompt.title': 'Unfinished session',
    'resumePrompt.description':
        'The previous run of this session never reported a finished task — it was interrupted, stopped, or crashed.',
    'resumePrompt.task': 'Task:',
    'resumePrompt.started': 'Started:',
    'resumePrompt.cliSession': 'CLI session:',
    'resumePrompt.startFresh': 'Start fresh',
    'resumePrompt.startFreshTooltip': 'Discard the saved conversation and start this session from scratch',
    'resumePrompt.continue': 'Continue',
    'resumePrompt.continueTooltip': 'Resume the saved CLI conversation (--resume) and pick the task back up',
    'resumePrompt.startedJustNow': '{abs} (just now)',
    'resumePrompt.startedMinutesAgo': '{abs} ({m}m ago)',
    'resumePrompt.startedHoursAgo': '{abs} ({h}h {m}m ago)',
    'resumePrompt.startedDaysAgo': '{abs} ({d}d ago)',
} as Record<string, string>;
