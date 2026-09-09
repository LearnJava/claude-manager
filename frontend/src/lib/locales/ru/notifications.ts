// Translation fragment: notifications (ru).
export default {
    'notify.taskComplete.title': 'Задача выполнена',
    'notify.taskComplete.body': '{id} — выполнено задач: {count}',
    'notify.rateLimited.title': 'Достигнут лимит запросов',
    'notify.rateLimited.body': '{id} — пауза до {until}',
    'notify.permissionNeeded.title': 'Требуется разрешение',
    'notify.permissionNeeded.body': '{id}: {tool}',
    'notify.questionNeedsAnswer.title': 'Нужен ответ на вопрос',
    'notify.questionNeedsAnswer.body': '{id}: {question}',
    'notify.sessionError.title': 'Ошибка сессии',
    'notify.sessionError.body': '{id}: {message}',
    'notify.unknownError': 'неизвестная ошибка',
    'notify.fallbackSession': 'сессия',
    'notify.fallbackReset': 'сброс',
    'notify.fallbackTool': 'инструмент',
    'notify.costRegression.title': 'Рост стоимости',
    'notify.costRegression.body': '{id} — {factor}x от недавней медианы',
} as Record<string, string>;
