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
    'sessionView.stopAfterTask.titleRequested':
        'Уже запрошено: остановится после завершения текущей задачи',
    'sessionView.stopAfterTask.titleDefault':
        'Мягкая остановка: дождаться завершения текущей задачи, затем остановить',
    'sessionView.stopAfterTask.stopping': '⏹ Остановка после задачи…',
    'sessionView.stopAfterTask.label': '⏹ Стоп после задачи',
    'sessionView.restart.label': 'Перезапуск',
    'sessionView.restart.title': 'Остановить и запустить сессию заново',
    'sessionView.copyLog.title': 'Скопировать весь видимый лог в буфер обмена',
    'sessionView.copyLog.copied': '✓ Скопировано',
    'sessionView.copyLog.label': '📋 Копировать лог',
    'sessionView.copyLog.errorPrefix': 'Копирование лога',
    'sessionView.clearLog.title': 'Очистить лог на экране (история сохраняется в хранилище)',
    'sessionView.clearLog.label': '🗑 Очистить лог',
    'sessionView.export.formatTitle': 'Формат экспорта',
    'sessionView.export.formatMarkdown': 'Markdown',
    'sessionView.export.formatJson': 'JSON',
    'sessionView.export.formatText': 'Текст',
    'sessionView.exportLog.title': 'Сохранить видимый лог в файл',
    'sessionView.exportLog.label': 'Экспорт лога',
    'sessionView.exportLog.savedPrefix': 'Сохранено →',
} as Record<string, string>;
