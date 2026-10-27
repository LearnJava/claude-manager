// Translation fragment: waiting (ru). Keys are namespaced 'waiting.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'waiting.title': 'Ждут тебя',
    'waiting.count': '({count})',
    'waiting.empty': 'Никто не ждёт тебя.',
    'waiting.resumesIn': 'продолжит через {time}',
    'waiting.statusLabel': 'Ждут тебя: {count}',
    'waiting.statusTooltip': 'Открыть экран всех сессий, которым нужен человек',
    'waiting.kind.permission': 'Ждёт разрешения',
    'waiting.kind.question': 'Задала вопрос и ждёт ответа',
    'waiting.kind.gates_exhausted': 'Ворота красные после всех кругов правок — остановлена',
    'waiting.kind.error': 'Остановилась с ошибкой',
    'waiting.kind.needs_human': 'Задача смешанного программирования требует человека',
    'waiting.kind.rate_limit': 'Упёрлась в лимит',
    'waiting.kind.stopped': 'Остановлена по запросу после задачи',
    'waiting.kind.queue_empty': 'Очередь опустела — брать нечего',
    'waiting.action.permission': 'Ответить…',
    'waiting.action.session': 'К сессии',
    'waiting.action.mixed': 'Открыть смешанный запуск',
} as Record<string, string>;
