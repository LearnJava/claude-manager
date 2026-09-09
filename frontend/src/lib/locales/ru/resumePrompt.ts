// Translation fragment: resumePrompt (ru). Keys are namespaced 'resumePrompt.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'resumePrompt.title': 'Незавершённая сессия',
    'resumePrompt.description':
        'Предыдущий запуск этой сессии не сообщил о завершении задачи — он был прерван, остановлен или произошёл сбой.',
    'resumePrompt.task': 'Задача:',
    'resumePrompt.started': 'Запущена:',
    'resumePrompt.cliSession': 'CLI-сессия:',
    'resumePrompt.startFresh': 'Начать заново',
    'resumePrompt.startFreshTooltip': 'Отбросить сохранённый диалог и начать эту сессию с нуля',
    'resumePrompt.continue': 'Продолжить',
    'resumePrompt.continueTooltip': 'Возобновить сохранённый диалог CLI (--resume) и продолжить задачу',
    'resumePrompt.startedJustNow': '{abs} (только что)',
    'resumePrompt.startedMinutesAgo': '{abs} ({m} мин назад)',
    'resumePrompt.startedHoursAgo': '{abs} ({h} ч {m} мин назад)',
    'resumePrompt.startedDaysAgo': '{abs} ({d} дн назад)',
} as Record<string, string>;
