// Translation fragment: waiting (en). Keys are namespaced 'waiting.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'waiting.title': 'Waiting for you',
    'waiting.count': '({count})',
    'waiting.empty': 'Nothing is waiting for you.',
    'waiting.resumesIn': 'resumes in {time}',
    'waiting.statusLabel': 'Waiting for you: {count}',
    'waiting.statusTooltip': 'Open the screen of every session that needs a person',
    'waiting.kind.permission': 'Waiting for a permission',
    'waiting.kind.question': 'Asked a question and waits for the answer',
    'waiting.kind.gates_exhausted': 'Gates are still red after all rework rounds — stopped',
    'waiting.kind.error': 'Stopped with an error',
    'waiting.kind.needs_human': 'Mixed-programming task needs a human',
    'waiting.kind.rate_limit': 'Hit the rate limit',
    'waiting.kind.stopped': 'Stopped after the task on request',
    'waiting.kind.queue_empty': 'Queue is empty — nothing left to take',
    'waiting.action.permission': 'Answer…',
    'waiting.action.session': 'Go to session',
    'waiting.action.mixed': 'Open mixed run',
} as Record<string, string>;
