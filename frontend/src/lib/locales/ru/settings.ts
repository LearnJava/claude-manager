// Translation fragment: settings (ru).
export default {
    'settings.language': 'Язык',

    // Header / tabs
    'settings.title': 'Настройки',
    'settings.tab.global': 'Общие',
    'settings.tab.projects': 'Проекты',
    'settings.tab.sessions': 'Сессии',
    'settings.tab.workers': 'Воркеры',

    // Loading / empty states
    'settings.loadingConfig': 'Загрузка конфигурации…',
    'settings.noConfigLoaded': 'Конфигурация не загружена.',

    // Shared field labels
    'settings.field.name': 'Название',
    'settings.field.model': 'Модель',
    'settings.common.remove': 'Удалить',

    // Global tab — General
    'settings.general.heading': 'Общие',
    'settings.general.claudePath': 'Путь к Claude CLI',
    'settings.general.theme': 'Тема',
    'settings.general.retryDelay': 'Задержка повтора (сек)',
    'settings.general.rateLimitPause': 'Пауза при лимите запросов (сек)',
    'settings.general.logRetention': 'Хранение логов (дней)',
    'settings.general.sessionStartDelay': 'Задержка запуска сессии (сек)',
    'settings.general.sessionStartDelayTitle': 'Прогрев кэша: разносить запуск сессий на это число секунд.',

    // Global tab — Crash recovery
    'settings.crashRecoveryGlobal.heading': 'Восстановление после сбоя',
    'settings.crashRecoveryGlobal.enable': 'Включить восстановление после сбоя',
    'settings.crashRecoveryGlobal.desc1': 'Если включено, менеджер сохраняет файл состояния сессии в',
    'settings.crashRecoveryGlobal.desc2': 'перед каждой задачей. Если приложение закрыто или упало во время работы сессии, следующий запуск продолжит прерванный диалог через',
    'settings.crashRecoveryGlobal.useForceNew1': 'Используйте',
    'settings.crashRecoveryGlobal.forceNew': 'Force new',
    'settings.crashRecoveryGlobal.useForceNew2': 'для каждой сессии (на вкладке «Сессии»), чтобы отбросить сохранённое состояние и начать заново.',

    // Global tab — Pre-flight analysis
    'settings.preflight.heading': 'Предварительный анализ',
    'settings.preflight.enable': 'Включить предварительный анализ',
    'settings.preflight.autoApproveSingle': 'Автоодобрение планов из одной сессии',
    'settings.preflight.analystModel': 'Модель аналитика',
    'settings.preflight.maxBudget': 'Макс. бюджет аналитика, USD (0 = без ограничения)',

    // Global tab — Permission timeouts
    'settings.permTimeouts.heading': 'Таймауты разрешений',
    'settings.permTimeouts.notifyAfter': 'Уведомить через (сек)',
    'settings.permTimeouts.timeout': 'Таймаут (сек, 0 = ждать бесконечно)',
    'settings.permTimeouts.timeoutAction': 'Действие по таймауту',
    'settings.permTimeouts.nativeNotification': 'Системное уведомление (Windows toast)',
    'settings.permTimeouts.soundAlert': 'Звуковой сигнал',

    // Global tab — Model routing
    'settings.modelRouting.heading': 'Маршрутизация моделей',
    'settings.modelRouting.autoRouting': 'Автовыбор модели (по сложности задачи через предварительный анализ)',

    // Global tab — Experience layer
    'settings.experienceLayer.heading': 'Слой опыта',
    'settings.experienceLayer.enable': 'Индексировать завершённые запуски во вкладку «Actions» (LEARN-TASKS.md LN-03)',
    'settings.experienceLayer.description': 'Извлекает из собственных CLI-транскриптов приложения нормализованные сигнатуры вызовов инструментов — без внешних сервисов, ничего не покидает эту машину. Выключено по умолчанию: при выключенной опции транскрипт вообще не открывается.',

    // Global tab — Budget alerts
    'settings.budgetAlerts.heading': 'Оповещения о бюджете',
    'settings.budgetAlerts.daily': 'Дневное оповещение (USD, 0 = выкл)',
    'settings.budgetAlerts.weekly': 'Недельное оповещение (USD, 0 = выкл)',
    'settings.budgetAlerts.rateLimitThreshold': 'Порог оповещения о лимите запросов',

    // Projects tab
    'settings.projects.countSingular': 'настроен {count} проект',
    'settings.projects.countPlural': 'настроено проектов: {count}',
    'settings.projects.addProjectButton': '+ Добавить проект',
    'settings.projects.emptyPart1': 'Проектов пока нет. Нажмите',
    'settings.projects.addProjectLabel': 'Добавить проект',
    'settings.projects.emptyPart2': ', чтобы создать первый.',
    'settings.projects.projectNumber': 'Проект #{n}',
    'settings.projects.path': 'Путь',
    'settings.projects.browse': 'Обзор…',
    'settings.projects.storageNote1': '📁 Сессии и проверки сохраняются в',
    'settings.projects.storageNote2': '(закоммитьте его, чтобы поделиться настройками проекта). Согласие на смешанное программирование хранится в',
    'settings.projects.storageNote3': '(в .gitignore, никогда не коммитится).',
    'settings.projects.storageNoteNoPath': 'Без папки проекта настройки остаются в глобальном конфиге.',
    'settings.projects.defaultPermMode': 'Режим разрешений по умолчанию для новых сессий',
    'settings.projects.inheritOption': '(наследовать → bypassPermissions)',
    'settings.projects.defaultPermModeHint': 'Задаёт значение только для сессий, добавляемых в этот проект впредь — у существующих сессий сохраняется своё значение (редактируется на вкладке «Сессии»).',
    'settings.projects.sessionCountSingular': '{count} сессия',
    'settings.projects.sessionCountPlural': 'сессий: {count}',
    'settings.projects.addSessionArrow': '+ Добавить сессию →',

    // Projects tab — Mixed programming
    'settings.mixed.enable': 'Включить смешанное программирование (внешние воркеры)',
    'settings.mixed.warning1': '⚠ Брифы и дословные фрагменты кода отправляются на внешние бесплатные эндпоинты, которые логируют запросы. Включайте только для проектов, чей код может покидать вашу машину.',
    'settings.mixed.warning2a': 'Это согласие хранится в',
    'settings.mixed.warning2b': 'и не коммитится — каждый участник команды соглашается сам за себя.',
    'settings.mixed.gatesLabel': 'Проверки (по одной команде на строку — блокирующие; ненулевой код возврата отклоняет раунд)',
    'settings.mixed.maxRounds': 'Максимум раундов обратной связи',

    // Projects tab — AI roadmap generation
    'settings.roadmap.intro1': '🤖 Опишите проект — и ИИ составит дорожную карту: разложит идею на бэклог задач размером с сессию, запишет',
    'settings.roadmap.intro2': 'в проект и настроит сессию "P1" на Sonnet для последовательной работы над ними.',
    'settings.roadmap.foundDraft': 'Найдена непроверенная дорожная карта из предыдущего запуска — {count} задач, уже сгенерирована (повторный запуск ничего не будет стоить).',
    'settings.roadmap.reviewButton': 'Просмотреть',
    'settings.roadmap.dismissButton': 'Скрыть',
    'settings.roadmap.ideaLabel': 'Идея проекта',
    'settings.roadmap.ideaPlaceholder': 'Что вы хотите построить?',
    'settings.roadmap.recommendedSuffix': ' (рекомендуется)',
    'settings.roadmap.generatingButton': 'Генерация… {elapsed}',
    'settings.roadmap.generateButton': 'Сгенерировать дорожную карту с ИИ',
    'settings.roadmap.startingAnalyst': 'Запуск сессии аналитика…',
    'settings.roadmap.setPathFirst': 'Сначала укажите папку проекта выше.',

    // Projects tab — Saved session logs
    'settings.logs.setPathFirst': 'Сохранённые логи: сначала укажите папку проекта выше.',
    'settings.logs.countSingular': 'Сохранённые логи: {count} файл, {size}',
    'settings.logs.countPlural': 'Сохранённые логи: файлов {count}, {size}',
    'settings.logs.unknown': 'Сохранённые логи: —',
    'settings.logs.refreshTitle': 'Обновить количество/размер сохранённых логов',
    'settings.logs.confirmDeleteTitle': 'Нажмите ещё раз, чтобы подтвердить удаление',
    'settings.logs.deleteTitle': 'Удалить все сохранённые файлы логов этого проекта',
    'settings.logs.confirmClearButton': 'Подтвердить удаление?',
    'settings.logs.clearButton': '🗑 Очистить логи проекта',
    'settings.logs.description': 'Каждая завершённая задача/запуск автоматически сохраняет свой лог сюда в формате markdown. Очистка удаляет эти файлы и их записи в SQLite — записи запусков в «Истории»/«Дашборде» сохраняются.',

    // Projects tab — Session protocol
    'settings.protocol.label': 'Протокол сессии',
    'settings.protocol.installTitle': 'Записать docs/git-workflow.md, scripts/worktree-pool.sh и навыки /cm-task-start, /cm-task-finish в этот проект. Существующие файлы никогда не перезаписываются.',
    'settings.protocol.installButton': 'Установить протокол сессии',
    'settings.protocol.description1': 'Правила, которым следует сессия, работающая по очереди задач: резервировать задачу веткой, работать в постоянном слоте worktree, делать merge',
    'settings.protocol.description2': 'после каждого коммита. Без этого прерванная сессия молча начинает задачу заново. Проекты, созданные из дорожной карты, получают это автоматически.',

    // Script-generated status/error messages (Projects tab actions)
    'settings.msg.selectProjectFolder': 'Выберите папку проекта',
    'settings.msg.folderPickerFailed': 'Не удалось открыть диалог выбора папки: {error}',
    'settings.msg.roadmapGenFailed': 'Не удалось сгенерировать дорожную карту: {error}',
    'settings.msg.roadmapWritten': 'ROADMAP.md и STATUS-P1.md записаны — сессия "P1" настроена.',
    'settings.msg.installedFiles': 'Установлено: {files}',
    'settings.msg.alreadyPresent': 'Уже существует — ничего не записано.',
    'settings.msg.protocolChecked': 'Протокол сессии проверен для {project}.',
    'settings.msg.installProtocolFailed': 'Не удалось установить протокол: {error}',
    'settings.msg.clearedLogs': 'Сохранённые логи для {project} очищены.',
    'settings.msg.clearLogsFailed': 'Не удалось очистить логи: {error}',
    'settings.msg.loadFailed': 'Не удалось загрузить: {error}',
    'settings.msg.saved': 'Сохранено.',
    'settings.msg.saveFailed': 'Не удалось сохранить: {error}',

    // Sessions tab
    'settings.sessions.addProjectFirst': 'Сначала добавьте проект (вкладка «Проекты»).',
    'settings.sessions.projectLabel': 'Проект',
    'settings.sessions.projectFallback': '(проект {n})',
    'settings.sessions.listHeading': 'Сессии',
    'settings.sessions.addButton': '+ Добавить',
    'settings.sessions.noSessions': 'Нет сессий.',
    'settings.sessions.sessionFallback': '(сессия {n})',
    'settings.sessions.selectOrAdd': 'Выберите или добавьте сессию.',

    'settings.sessions.identityHeading': 'Идентификация',
    'settings.sessions.taskSourceFile': 'Файл источника задач',
    'settings.sessions.promptLabel': 'Промпт',

    'settings.sessions.modelHeading': 'Модель',
    'settings.sessions.effort': 'Уровень',
    'settings.sessions.fallbackModel': 'Резервная модель',
    'settings.sessions.fallbackModelTitle': 'Модель, используемая при превышении лимита запросов или перегрузке основной модели.',
    'settings.sessions.fallbackToggleTitle': 'При лимите запросов: сразу переключиться на fallback_model и перезапустить без ожидания. Повторяет поведение fallback в orchestrator.py.',
    'settings.sessions.fallbackToggleLabel': 'Переключаться на резервную модель при лимите запросов (без ожидания)',

    'settings.sessions.permissionsHeading': 'Разрешения',
    'settings.sessions.permissionMode': 'Режим разрешений',
    'settings.sessions.allowedTools': 'Разрешённые инструменты (по одному на строку)',
    'settings.sessions.disallowedTools': 'Запрещённые инструменты (по одному на строку)',
    'settings.sessions.autoApproveRules': 'Правила автоодобрения',
    'settings.sessions.addRule': '+ Добавить правило',
    'settings.sessions.noRules': 'Нет правил.',

    'settings.sessions.lifecycleHeading': 'Жизненный цикл',
    'settings.sessions.maxBudget': 'Макс. бюджет, USD (0 = без ограничения)',
    'settings.sessions.maxTasks': 'Макс. число задач (0 = без ограничения)',
    'settings.sessions.autoRestart': 'Автоперезапуск после задачи',
    'settings.sessions.stopWhenNoTasks': 'Останавливаться при отсутствии задач',
    'settings.sessions.useWorktree': 'Использовать git worktree',
    'settings.sessions.preflightLabel': 'Предварительный анализ',

    'settings.sessions.hooksHeading': 'Хуки',
    'settings.sessions.preTaskHook': 'Хук перед задачей',
    'settings.sessions.postTaskHook': 'Хук после задачи',

    'settings.sessions.crashRecoveryHeading': 'Восстановление после сбоя',
    'settings.sessions.recoveryPrompt': 'Промпт восстановления',
    'settings.sessions.recoveryPromptPlaceholder': '(по умолчанию: проверить git status и продолжить прерванную задачу)',
    'settings.sessions.recoveryPromptTitle': 'Сообщение, отправляемое Claude при возобновлении прерванной сессии. Оставьте пустым, чтобы использовать встроенное значение по умолчанию.',

    'settings.sessions.contextHeading': 'Контекст',
    'settings.sessions.appendSystemPrompt': 'Добавить к системному промпту',
    'settings.sessions.additionalDirs': 'Дополнительные каталоги (по одному на строку)',

    // Workers tab
    'settings.workers.listHeading': 'Воркеры',
    'settings.workers.addButton': '+ Добавить',
    'settings.workers.noWorkers': 'Нет воркеров.',
    'settings.workers.fallback': '(воркер {n})',
    'settings.workers.addFromPreset': 'Добавить из пресета',
    'settings.workers.selectOrAdd': 'Выберите или добавьте воркера.',
    'settings.workers.role': 'Роль',
    'settings.workers.baseUrl': 'Base URL (шлюз, совместимый с OpenAI)',
    'settings.workers.modelId': 'ID модели',
    'settings.workers.apiKeyEnv': 'Переменная окружения с API-ключом',
    'settings.workers.apiKeyEnvTitle': 'API-ключ читается из этой переменной окружения — никогда не сохраняется в конфиге.',
    'settings.workers.reasoningEffort': 'Уровень рассуждений',
    'settings.workers.maxOutputTokens': 'Макс. токенов на выходе',
    'settings.workers.continuationCap': 'Лимит продолжений',
    'settings.workers.continuationCapTitle': 'Максимум продолжений при finish_reason=length, прежде чем сдаться (защита от зацикливания).',
    'settings.workers.requestTimeout': 'Таймаут запроса (сек)',
    'settings.workers.asciiAnchorsTitle': 'Предупреждать при генерации брифа, что его якоря FIND должны быть чистым ASCII (кириллические якоря ломают некоторые модели).',
    'settings.workers.asciiAnchorsLabel': 'Только ASCII-якоря FIND',

    // Footer
    'settings.footer.changesNote': 'Изменения записываются в TOML при сохранении.',
    'settings.footer.saving': 'Сохранение…',
} as Record<string, string>;
