// Translation fragment: hookRules (en). Keys are namespaced 'hookRules.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'hookRules.title': 'Hook rules',
    'hookRules.suggest': 'Suggest rules',
    'hookRules.intro':
        'A hook rule stops a wasted call before it runs (zero tokens, works on step 200 as on step 1) and says what to do instead. Nothing is written into the repository until you install a rule; it then runs on trial and is taken out again if runs get longer or more erroneous.',
    'hookRules.empty': 'No rules yet — "Suggest rules" proposes some from this project\'s history.',
    'hookRules.kind.repeat_read': 'Forbid repeating a read/search with no edit in between',
    'hookRules.kind.empty_search': 'Stop guessing after two empty searches',
    'hookRules.kind.oversized_read': 'Forbid whole-file reads over (tokens)',
    'hookRules.kind.failure_cmd': 'Forbid a command that keeps failing',
    'hookRules.showDiff': 'Show diff',
    'hookRules.install': 'Install into the repository',
    'hookRules.dismiss': 'Dismiss',
    'hookRules.remove': 'Take out',
    'hookRules.fileWrite': 'Writes',
    'hookRules.fileDelete': 'Deletes',
    'hookRules.status.trial': 'on trial ({runs} runs)',
    'hookRules.status.kept': 'kept',
    'hookRules.effect': 'Steps per run {stepsBefore} → {stepsAfter}, errors per run {errorsBefore} → {errorsAfter} ({runs} runs since install)',
    'hookRules.why.worse': 'taken out: runs got longer or more erroneous',
    'hookRules.why.reverted_manual': 'taken out by hand',
};
