// Translation fragment: sessionView (en). Keys are namespaced 'sessionView.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'sessionView.gitInitBanner.needsPrefix': 'This session needs',
    'sessionView.gitInitBanner.butSuffix': ', but',
    'sessionView.gitInitBanner.notUsable':
        "isn't a usable git repository yet (not initialized, or has no commits — a bare",
    'sessionView.gitInitBanner.aloneNotEnough': "alone isn't enough).",
    'sessionView.gitInit.initializing': 'Initializing…',
    'sessionView.gitInit.button': 'Initialize git repo & retry',
    'sessionView.gitInit.failedPrefix': 'Init git repo failed',
    'sessionView.stop.label': 'Stop',
    'sessionView.stop.title': 'Stop: terminate the process now',
    'sessionView.stopAfterTask.titleRequested':
        'Already requested: will stop once the current task finishes',
    'sessionView.stopAfterTask.titleDefault': 'Soft stop: let the current task finish, then stop',
    'sessionView.stopAfterTask.stopping': '⏹ Stopping after task…',
    'sessionView.stopAfterTask.label': '⏹ Stop after task',
    'sessionView.restart.label': 'Restart',
    'sessionView.restart.title': 'Stop and start the session again',
    'sessionView.copyLog.title': 'Copy the full visible log to clipboard',
    'sessionView.copyLog.copied': '✓ Copied',
    'sessionView.copyLog.label': '📋 Copy log',
    'sessionView.copyLog.errorPrefix': 'Copy log',
    'sessionView.clearLog.title': 'Clear the on-screen log buffer (history is preserved in storage)',
    'sessionView.clearLog.label': '🗑 Clear log',
    'sessionView.export.formatTitle': 'Export format',
    'sessionView.export.formatMarkdown': 'Markdown',
    'sessionView.export.formatJson': 'JSON',
    'sessionView.export.formatText': 'Text',
    'sessionView.exportLog.title': 'Save the visible log to a file',
    'sessionView.exportLog.label': 'Export log',
    'sessionView.exportLog.savedPrefix': 'Saved →',
} as Record<string, string>;
