# COST-TASKS — дешевле ожидание, журнал и длинные интерактивные запуски; честный замер

Блок по итогам замера «после» (`docs/Science_lab/MEASURE.md`, окно 14–23.10.2026).
Передача задачи по бюджету контекста (LN-28) и UTF-8 сработали; остались четыре
утечки, которые чинятся в коде claude-manager.

## Почему (2026-10-23, Chat)

Разбор транскриптов Lumen и claude-manager за 14–23.10, включая субагентов.
Скрипты разбора (не в репозитории, только для справки):
`C:/kostja/GoProjects/claude-manager/.tmp/deep_after.py`, `deep_cache.py`, `deep_poll.py`.

1. **Ожидание через `sleep`.** В Lumen 540 вызовов `sleep 60; cat log` в 109 из 215
   транскриптов, 49 часов сна, ~$60 из $504 (12%) — каждый опрос заново перечитывает
   весь контекст. Автономным сессиям это прямо советует `backgroundTaskWarningPrompt`
   (`internal/session/session.go`): «poll its output file yourself with short waits».
2. **Журнал задач идёт на opus.** `runJournalEntry` (`internal/session/manager.go`)
   берёт `Settings.PreflightModel`; у пользователя `preflight_model = "opus"`, поэтому
   все 18 записей журнала Lumen за неделю — opus, хотя `config.example.toml` и
   `BuildJournalArgs` обещают haiku.
3. **Бюджет контекста не действует в интерактивных запусках.** Сессия Lumen P6
   стартовала с пустой очередью (`forceInteractive` → `autonomous = false`,
   `session.go` ~стр. 1550), пользователь дал ей 30 задач в том же запуске —
   247 вызовов, контекст до 318k, ни одной заметки о передаче. Chat-сессии
   claude-manager доходят до 207–277k. Существующий `context_handoff` срабатывает
   от 0.75 × окна, а при окне 1M это 750k — никогда.
4. **Замер врёт.** `measure.py` не читает `<session>/subagents/*.jsonl` (Lumen: $408
   вместо $504), записывает вывод составной команды (`git pull -q …; sed …; cat …`)
   на первую команду (отсюда «git pull 3150 токенов»), а «Read *.md» в Lumen
   показывает «—» при 39 таких чтениях.

## Инварианты блока

- Всё новое выключено по умолчанию или включается уже существующей настройкой
  (`handoff_context_tokens > 0`); при выключенной настройке промпт и поведение —
  байт в байт как до блока.
- Ничего не записывается в репозиторий пользователя без подтверждения в UI.
- Базовая линия «до» (`docs/Science_lab/measurements/2026-10-13-before-*`) не
  перезаписывается: транскрипты старше 30 дней уже удалены.

## Как сессия берёт задачу

Та же схема, что у `CTX-TASKS.md`: указатель `COST-TASKS.md:NN` в
[STATUS-P1.md](./STATUS-P1.md), статус — таблица в конце файла, протокол —
`/cm-task-start` и `/cm-task-finish` по [docs/git-workflow.md](./docs/git-workflow.md).
Гейт: `go build ./... && go vet ./... && go test ./...` (+ `npm --prefix frontend test`,
если трогали фронтенд). Новые задачи дописывать в конец файла, чтобы не сдвигать
указатели.

---

## COST-01: автономная сессия ждёт долгую команду одним вызовом, а не опросами

**Depends on:** —

**Files:**
- `internal/session/session.go` — константа `backgroundTaskWarningPrompt` и комментарий над ней (~стр. 2540–2561)
- `internal/session/handoff_budget_test.go`, `internal/session/hermes_runtime_test.go` — если сравнивают текст
- `docs/design-decisions.md` — абзац про `backgroundTaskWarningPrompt` (~стр. 1943)

**What to do:**
1. Переписать вторую половину `backgroundTaskWarningPrompt`: вместо «poll its output
   file yourself with short waits» — «wait in one blocking call»: запускать долгую
   команду в foreground с большим `timeout` Bash-инструмента (до 600000 мс), а если
   команда пишет в лог — блокироваться на нём одним вызовом, например
   `timeout 590 bash -c 'until grep -qE "Finished|error" <log>; do sleep 10; done'; tail -n 20 <log>`.
   Объяснить причину одной фразой: каждый вызов модели перечитывает весь разговор,
   поэтому `sleep N; cat log` в цикле — самый дорогой способ ждать. Первую половину
   (процесс убивается в конце хода, `run_in_background` не переживёт ход) оставить.
2. Последнюю ветку («если не успевает за ход — прогресс в файл для следующей
   сессии») оставить.
3. Комментарий над константой и абзац в `docs/design-decisions.md` — сослаться на
   замер (Lumen 14–23.10: 540 опросов, ~12% затрат).
4. Тесты, которые собирают ожидаемый промпт из константы, править не нужно; если
   какой-то тест сверяет литерал — обновить.

**Done when:**
- Гейт зелёный.
- В `backgroundTaskWarningPrompt` нет совета опрашивать файл короткими ожиданиями,
  есть рецепт блокирующего ожидания.
- Строка COST-01 в таблице Status — `✓ DONE`, указатель удалён из `STATUS-P1.md`.

---

## COST-02: отдельная модель для журнала задач, по умолчанию haiku

**Depends on:** —

**Files:**
- `internal/config/types.go` — `GlobalSettings`: новое поле `JournalModel string \`toml:"journal_model"\`` рядом с `PreflightModel`
- `internal/session/manager.go` — `runJournalEntry` (~стр. 2259): `Model:` из `JournalModel`
- `internal/session/manager_test.go` (или тест журнала рядом) — проверка выбора модели
- `config.example.toml` — строка `journal_model` в `[settings]` рядом с `preflight_model`, с комментарием
- `PLAN.md` — раздел с `preflight_model` (~стр. 175 и ~стр. 332): новое поле
- Форма настроек, если глобальные поля `[settings]` там перечислены явно (`frontend/src/…Settings…`) — поле «Модель журнала»; если форма сохраняет неизвестные поля как есть, достаточно бэкенда

**What to do:**
1. `runJournalEntry` передаёт `Model: m.cfg.Settings.JournalModel`. Пустое значение →
   `BuildJournalArgs` и так подставляет `"haiku"`; наследование от `PreflightModel`
   убрать.
2. Тест: при `PreflightModel = "opus"` и пустом `JournalModel` аргументы журнала
   содержат `--model haiku`; при `JournalModel = "sonnet"` — `--model sonnet`.
3. Doc-sync по матрице CLAUDE.md (новое поле `GlobalSettings` → `config.example.toml`
   + PLAN.md).

**Done when:**
- Гейт зелёный, новый тест падает на старом коде.
- Журнал в Lumen после пересборки пишется haiku (видно в `.claude-manager/logs` или в
  транскрипте записи журнала: `"model":"claude-haiku-…"`).
- Строка COST-02 в таблице Status — `✓ DONE`, указатель удалён из `STATUS-P1.md`.

---

## COST-03: бюджет контекста в интерактивном запуске — вопрос пользователю

**Depends on:** —

**Files:**
- `internal/session/session.go` — `checkContextRestart` (~стр. 2091), `askContextRestart` (~стр. 2160), место, где на конец хода берётся `lastCallUsage`
- `internal/session/handoff_budget.go` — комментарий в шапке (описать интерактивную ветку)
- `internal/session/session_contexthandoff_test.go` (или новый `handoff_budget_interactive_test.go`)
- `docs/design-decisions.md` — раздел про context-budget handoff
- `config.example.toml` — комментарий к `handoff_context_tokens`

**What to do:**
1. В конце хода **неавтономного** запуска (`autonomous == false`, не per-turn runtime)
   при `Config.HandoffContextTokens > 0`: если контекст последнего вызова
   (`input + cache_read + cache_creation` из `lastCallUsage`, как в CTX-01) ≥
   `HandoffContextTokens` — задать пользователю вопрос через существующий
   `askContextRestart` (сжать `/compact` / перезапуск с recap / продолжить). Не
   зависит от `context_handoff` и `context_restart_mode`: в интерактивном запуске
   решает пользователь. Агенту ничего не подмешивается — ни в системный промпт, ни
   в stdin.
2. Вопрос — один раз за процесс; после «продолжить» — повторить, только когда
   контекст вырастет ещё на половину бюджета (150k → снова на 225k), не на каждом ходе.
   Новый процесс (рестарт/recap) сбрасывает счётчик.
3. Текст вопроса для этой ветки — в токенах, не в процентах окна: «Контекст
   последнего вызова 160k токенов (бюджет 150k) — каждый следующий ход перечитывает
   всё. Что сделать?» (на английском, как остальные строки `askContextRestart`).
   Если `askContextRestart` принимает только `util float64` — добавить параметр
   или отдельную функцию формирования текста.
4. Если в этом же ходе уже сработал обычный `checkContextRestart` (порог 0.75 окна) —
   второй вопрос не задавать.
5. Тесты: интерактивная сессия с `HandoffContextTokens = 150000`, ход с последним
   вызовом 160k → в `PendingQuestion` появился вопрос с вариантами
   `contextRestartOptions`; 140k → вопроса нет; `HandoffContextTokens = 0` → вопроса
   нет и поведение прежнее; автономный запуск → эта ветка не срабатывает (там
   работает `checkHandoffBudget`).
6. Doc-sync: `docs/design-decisions.md`, комментарий в `config.example.toml`
   («в интерактивном запуске — вопрос пользователю в конце хода»).

**Done when:**
- Гейт зелёный, тесты п. 5 проходят.
- В работающем менеджере у сессии с очередью, запущенной при пустой очереди, и у
  Chat-сессии с заданным `handoff_context_tokens` после хода с контекстом выше
  бюджета в карточке появляется вопрос о сжатии/перезапуске.
- Строка COST-03 в таблице Status — `✓ DONE`, указатель удалён из `STATUS-P1.md`.

---

## COST-04: `measure.py` — субагенты, составные команды, опросы и перестройки кэша

**Depends on:** —

**Files:**
- `docs/Science_lab/claude-code-setup/tools/measure/measure.py` — `transcript_files`, `scan`, `build_metrics`, `summary_md`, `compare` (таблица метрик ~стр. 391)
- `docs/Science_lab/claude-code-setup/tools/measure/cost_attribution.py` — категории инструментов (`category`), если там причина «Read *.md = —»
- `docs/Science_lab/MEASURE.md` — раздел «Что смотреть в сравнении», журнал изменений
- `docs/Science_lab/measurements/2026-10-23-after-lumen/`, `2026-10-23-after-claude-manager/` — пересчитать

**What to do:**
1. **Субагенты.** `transcript_files` добавляет `<папка>/<session-id>/subagents/*.jsonl`
   для папки проекта и всех папок `--claude-worktrees-*`. Затраты, вызовы и доли
   компонентов считают их; «Сессий», «Вызовов на сессию p90», «Сессии >100 вызовов» —
   только транскрипты верхнего уровня. Новая строка метрик «$ субагентов, %».
   `cost_attribution.py` и `session_length_cost.py` получают тот же список файлов.
2. **Составные команды.** Вывод Bash-команды относить к категории команды, только
   если после снятия ведущих `cd …;`/`cd … &&` в строке одна команда (пайпы `|`
   внутри не считаются разделителем; `;`, `&&`, `||`, перевод строки — считаются).
   Иначе — категория «Bash: составная». Метрика «git pull, ср. токенов» после этого
   считает только чистый `git pull`.
3. **Read *.md.** Найти, почему в срезе Lumen `"Read: *.md": null` при 39 вызовах Read
   по `.md` (сверить с `deep_after.py`, блок [F]), починить.
4. **Новые метрики** в `metrics.json`, `summary.md` и таблице `compare`:
   - «Опросы sleep»: число Bash-вызовов, начинающихся с `sleep N` (после ведущего
     `cd`), сумма N в часах и $ вызовов модели сразу после такого опроса
     (логика — `deep_poll.py`).
   - «Перестройки кэша»: вызовы не первые в транскрипте, где
     `cache_creation_input_tokens > 20000` и > 50% контекста вызова; число и $ записи
     (логика — `deep_after.py` блок [B]).
   - Запись кэша 5m / 1h в млн токенов.
   Ожидание в колонке «Что должно её сдвинуть»: опросы — COST-01 и правило kit
   «Wait for a long command in one blocking call»; перестройки — то же + субагенты
   на sonnet в Lumen.
5. **Пересчитать «после»** с теми же метками (перезаписать):
   `snapshot <проект> --since 2026-10-14 --until 2026-10-24 --label 2026-10-23-after-<lumen|claude-manager>`
   и `compare` с `2026-10-13-before-*`. «До» не пересчитывать (инвариант); новые метрики
   там будут «—», в `compare` это допустимо.
6. **MEASURE.md**: строки новых метрик в «Что смотреть в сравнении», строка журнала
   изменений «2026-10-23 — measure.py: субагенты, составные команды, опросы,
   перестройки кэша — исправление замера», ссылки на пересчитанные срезы.

**Done when:**
- `measure.py snapshot` для Lumen с `--since 2026-10-14 --until 2026-10-24` даёт
  затраты ≈ $500 (с субагентами; без них было $408), «$ субагентов» ≈ 19%,
  «git pull, ср. токенов» < 500, «Read *.md» не «—», «Опросы sleep» ≈ 540.
- `compare` печатает новые строки без ошибок при «—» в «до».
- `MEASURE.md` обновлён, срезы закоммичены.
- Строка COST-04 в таблице Status — `✓ DONE`, указатель удалён из `STATUS-P1.md`.

---

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| COST-01 | ✓ DONE | internal/session/session.go |
| COST-02 | ✓ DONE | internal/config/types.go, internal/session/manager.go |
| COST-03 | ○ TODO | internal/session/session.go, handoff_budget.go |
| COST-04 | ○ TODO | docs/Science_lab/claude-code-setup/tools/measure/measure.py |
