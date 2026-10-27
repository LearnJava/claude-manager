// Translation fragment: statusBar (en). Keys are namespaced 'statusBar.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'statusBar.activeLabel': 'Active:',
    'statusBar.openQueueTooltip': 'Open permission queue',
    'statusBar.waitingLabel': 'Waiting: {count}',
    'statusBar.rateLimitedLabel': 'Rate limited: {count}',
    'statusBar.errorsLabel': 'Errors: {count}',
    'statusBar.openDashboardTooltip': 'Open cost dashboard',
    'statusBar.regressionsLabel': 'Regressions: {count}',
    'statusBar.todayTooltip': 'Today: {split} — click to switch units',
    'statusBar.todayLabel': 'Today:',
    'statusBar.tokUnit': 'tok',
    'statusBar.todaySubscriptionTooltip': 'Today, tokens taken out of the limit window (input + cache write + cache read): {split}',
    'statusBar.windowShareOf': '{pct} of window',
    'statusBar.windowUnknown': 'share unknown',
    'statusBar.costConventional': '≈ {cost} conv. units',
    'statusBar.rlLabel': 'RL:',
    'statusBar.rlTooltip': 'Global rate-limit utilization',
    'statusBar.uptimeLabel': 'Uptime:',
} as Record<string, string>;
