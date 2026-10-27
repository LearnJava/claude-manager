// Translation fragment: hookRules (ru). Keys are namespaced 'hookRules.xxx' — see frontend/src/lib/i18n.ts.

export default {
    'hookRules.title': 'Правила-хуки',
    'hookRules.suggest': 'Предложить правила',
    'hookRules.intro':
        'Правило-хук останавливает лишний вызов до запуска (ноль токенов, на 200-м шаге так же, как на первом) и объясняет, что делать вместо него. В репозиторий ничего не пишется, пока вы не установите правило; дальше оно идёт на испытание и снимается, если запуски стали длиннее или ошибок больше.',
    'hookRules.empty': 'Правил пока нет — «Предложить правила» подберёт их по истории проекта.',
    'hookRules.kind.repeat_read': 'Запретить повтор чтения/поиска без правок между ними',
    'hookRules.kind.empty_search': 'Не гадать после двух пустых поисков',
    'hookRules.kind.oversized_read': 'Запретить чтение файла целиком выше (токенов)',
    'hookRules.kind.failure_cmd': 'Запретить команду, которая раз за разом падает',
    'hookRules.showDiff': 'Показать дифф',
    'hookRules.install': 'Установить в репозиторий',
    'hookRules.dismiss': 'Отклонить',
    'hookRules.remove': 'Снять',
    'hookRules.fileWrite': 'Записывает',
    'hookRules.fileDelete': 'Удаляет',
    'hookRules.status.trial': 'испытание ({runs} запусков)',
    'hookRules.status.kept': 'оставлено',
    'hookRules.effect': 'Шагов на запуск {stepsBefore} → {stepsAfter}, ошибок на запуск {errorsBefore} → {errorsAfter} ({runs} запусков после установки)',
    'hookRules.why.worse': 'снято: запуски стали длиннее или с большим числом ошибок',
    'hookRules.why.reverted_manual': 'снято вручную',
};
