// Translation fragment: sessionView (ru). Keys are namespaced 'sessionView.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'sessionView.gitInitBanner.needsPrefix': 'Этой сессии нужен',
    'sessionView.gitInitBanner.butSuffix': ', но',
    'sessionView.gitInitBanner.notUsable':
        'пока не является пригодным git-репозиторием (не инициализирован либо нет коммитов — простого',
    'sessionView.gitInitBanner.aloneNotEnough': 'недостаточно).',
    'sessionView.gitInit.initializing': 'Инициализация…',
    'sessionView.gitInit.button': 'Инициализировать git-репозиторий и повторить',
    'sessionView.gitInit.failedPrefix': 'Не удалось инициализировать git-репозиторий',
    'sessionView.stop.label': 'Стоп',
    'sessionView.stop.title': 'Стоп: завершить процесс немедленно',
    'sessionView.interrupt.label': 'Прервать',
    'sessionView.interrupt.title':
        'Прервать текущее действие; сессия остаётся открытой и ждёт вашего следующего сообщения',
    'sessionView.stopAfterTask.titleRequested':
        'Уже запрошено: остановится после завершения текущей задачи',
    'sessionView.stopAfterTask.titleDefault':
        'Мягкая остановка: дождаться завершения текущей задачи, затем остановить',
    'sessionView.stopAfterTask.stopping': '⏹ Остановка после задачи…',
    'sessionView.stopAfterTask.label': '⏹ Стоп после задачи',
    'sessionView.restart.label': 'Перезапуск',
    'sessionView.restart.title': 'Остановить и запустить сессию заново',
    'sessionView.restart.dialogTitle': 'Перезапустить сессию',
    'sessionView.restart.continue': 'Продолжить старую сессию',
    'sessionView.restart.fresh': 'Начать новую',
    'sessionView.restart.cancel': 'Отмена',
} as Record<string, string>;
