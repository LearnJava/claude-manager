// Translation fragment: notifications (en) — Windows toast titles/bodies
// fired from stores/sessions.ts (notify()), a plain .ts module (not a
// Svelte component), so it reads the current locale via get(t) instead of
// the $t auto-subscription components use.
export default {
    'notify.taskComplete.title': 'Task complete',
    'notify.taskComplete.body': '{id} — {count} task(s) done',
    'notify.rateLimited.title': 'Rate limited',
    'notify.rateLimited.body': '{id} — paused until {until}',
    'notify.permissionNeeded.title': 'Permission needed',
    'notify.permissionNeeded.body': '{id}: {tool}',
    'notify.questionNeedsAnswer.title': 'Question needs an answer',
    'notify.questionNeedsAnswer.body': '{id}: {question}',
    'notify.sessionError.title': 'Session error',
    'notify.sessionError.body': '{id}: {message}',
    'notify.unknownError': 'unknown error',
    'notify.fallbackSession': 'session',
    'notify.fallbackReset': 'reset',
    'notify.fallbackTool': 'tool',
    'notify.costRegression.title': 'Cost regression',
    'notify.costRegression.body': '{id} — {factor}x recent median',
    'notify.budgetDaily.title': 'Daily budget exceeded',
    'notify.budgetDaily.body': 'Spent ${current} today (alert at ${limit})',
    'notify.budgetWeekly.title': 'Weekly budget exceeded',
    'notify.budgetWeekly.body': 'Spent ${current} in the last 7 days (alert at ${limit})',
} as Record<string, string>;
