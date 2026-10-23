# Слой опыта — план работ (LN-01…LN-18)

Проект накапливает историю (`session_runs`, `session_logs`, транскрипты CLI,
раунды mixed-программирования) и не извлекает из неё ничего. Этот блок задач
превращает историю в три вещи, которые экономят токены и время следующей
сессии: **разрешения**, которые больше не спрашиваются; **контекст**, который
не приходится переоткрывать; **скиллы** — дистиллированные процедуры, которые
висят в контексте одной строкой описания и подгружаются телом только по
требованию.

Ключевая ставка: у приложения уже есть два самых редких ингредиента для такого
обучения — **верификатор** (`internal/worker/gates.go`, ворота проекта) и
**БД исходов** (`session_runs` со стоимостью, токенами, статусом). Без них
любая «память агента» вырождается в свалку правдоподобных советов.

Каждая задача рассчитана на одну Claude Code сессию (~15-30 turns, ~5-10 файлов).
Правило проекта: каждая функция сопровождается тестом.

## Как сессия берёт задачу

Схема трекинга — канонная схема проекта (та же, что в `MIXED-TASKS.md`):
очередь = голые указатели, мастер-список = этот файл, статусы = таблица Status ниже.

**Очередь: [STATUS-P1.md](./STATUS-P1.md)** (до LN-18 очередью был
[LEARN-STATUS.md](./LEARN-STATUS.md), сейчас он пуст — блок LN-01…LN-18 закрыт)
— только строки-указатели
`LEARN-TASKS.md:NN` на заголовки открытых задач, приоритет сверху вниз, ничего
больше. `hasTasks()` манагера понимает этот формат, поэтому очередь указывается
как `task_source` — цикл сам остановится, когда файл опустеет. Порядок в
очереди — по зависимостям, а не по номерам (LN-18 идёт после LN-05).
Завершённая задача = строка удаляется; статус «в работе» живёт в таблице
Status ниже, не в очереди — там нет ни заголовков, ни комментариев.

Правила для сессии:

1. **Старт — `/cm-task-start <имя>`.** Скилл синхронизируется с origin, проверяет
   по `git branch`, не занята ли задача, и занимает слот пула веткой
   `p<N>-<имя>`. **Существующая ветка `p<N>-…` — это твоя прерванная задача:
   продолжай ЕЁ**, а не бери новую (таблица Status — вторичный признак, git
   первичен). Иначе — верхний указатель из `LEARN-STATUS.md`, все зависимости
   которого уже `✓ DONE`, и `○ TODO` → `● IN PROGRESS`.
2. Выполни задачу по её спецификации в этом файле. Каждая функция — с тестом.
   `go build ./...` перед каждым коммитом; merge в master + push **после каждого
   коммита**, а не в конце задачи.
3. **Завершение — `/cm-task-finish`.** Скилл гоняет полный гейт, синхронизирует
   документацию по матрице doc-sync, снимает указатель с очереди, ставит
   `✓ DONE (дата)`, вливает ветку `--no-ff` в master, пушит и освобождает слот.
   Шаги здесь не дублируются: единственный источник — сам скилл и
   [docs/git-workflow.md](./docs/git-workflow.md).
4. Одна задача = одна сессия. Не бери следующую.

**Переиндексация (обязательно):** вставил K строк в этот файл выше строки L —
сдвинь на +K все указатели `LEARN-TASKS.md:NN` с `NN ≥ L` в `LEARN-STATUS.md`.
Проверка указателя: `sed -n 'NNp' LEARN-TASKS.md` должен показать заголовок
`## LN-…`. Дописывай новые задачи в конец файла — сдвига не будет.

**Конфиг сессии в claude-manager:**

```toml
[[session]]
name = "Developer 1"
prompt = "Ты программист 1. Читай CLAUDE.md, раздел «Roles and session start», и работай по очереди STATUS-P1.md."
task_source = "STATUS-P1.md"
stop_when_no_tasks = true
auto_restart = true
use_worktree = false   # worktree заводит сам протокол (scripts/worktree-pool.sh),
                       # менеджерский флаг сделал бы второй, одноразовый
```

Промпт — одна строка сознательно: протокол живёт в репозитории (версионируется,
виден в диффе, правится одной сессией для всех), а не в конфиге менеджера.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| LN-01 | ✓ DONE (2026-09-08) | internal/experience/transcript.go, testdata/transcripts/ |
| LN-02 | ✓ DONE (2026-09-08) | internal/experience/signature.go, internal/store/migrations.go, store.go |
| LN-03 | ✓ DONE (2026-09-08) | internal/experience/indexer.go, manager.go, app.go, ExperiencePanel.svelte |
| LN-04 | ✓ DONE (2026-09-09) | internal/experience/allowlist.go, internal/store (permission_events), app.go |
| LN-05 | ✓ DONE (2026-09-09) | internal/experience/primer.go, internal/session/session.go, config/types.go |
| LN-06 | ✓ DONE (2026-09-09) | internal/experience/journal.go, internal/analysis/journal.go |
| LN-07 | ✓ DONE (2026-09-09) | internal/experience/failures.go |
| LN-08 | ✓ DONE (2026-09-09) | internal/experience/candidate.go |
| LN-09 | ✓ DONE (2026-09-09) | internal/analysis/skill.go, schema.go, internal/store (skills) |
| LN-10 | ✓ DONE (2026-09-09) | internal/experience/skillfiles.go, internal/store (ListSkills/UpdateSkillApproved/UpdateSkillArchived), app.go, frontend SkillReview.svelte, ExperiencePanel.svelte |
| LN-11 | ✓ DONE (2026-09-09) | internal/experience/skillquality.go, internal/store (RunsWithSignature), app.go, frontend SkillReview.svelte |
| LN-12 | ✓ DONE (2026-09-09) | internal/experience/attribution.go |
| LN-13 | ✓ DONE (2026-09-09) | internal/optimization/routing.go, outcomes.go, internal/store/store.go |
| LN-14 | ✓ DONE (2026-09-09) | internal/optimization/cache.go, internal/experience/affinity.go |
| LN-15 | ✓ DONE (2026-09-09) | internal/experience/handoff.go, internal/optimization/context.go |
| LN-16 | ✓ DONE (2026-09-09) | internal/experience/regression.go, internal/session/manager.go, app.go, CostDashboard.svelte, StatusBar.svelte |
| LN-17 | ✓ DONE (2026-09-08) | internal/experience/mdlog.go, indexer.go (IngestDir), testdata/logfiles/ |
| LN-18 | ✓ DONE (2026-09-09) | internal/experience/duration.go, primer.go, ExperiencePanel.svelte |
| LN-19 | ✓ DONE (2026-09-15) | internal/experience/indexer.go (projectPath в actionRows), signature.go (sanitizeForeignPath) |
| LN-20 | ✓ DONE (2026-09-15) | app.go (ImportProjectLogs), internal/session/manager.go, ExperiencePanel.svelte, stores/experience.ts |
| LN-21 | ✓ DONE (2026-09-15) | internal/session/session.go (EvtTaskDone rotation-order fix), internal/experience/indexer.go (IngestRun md fallback), internal/session/manager.go |
| LN-22 | ✓ DONE (2026-09-15) | internal/experience/candidate.go (stepOccurrenceWeight, outcomeWeight) |
| LN-23 | ✓ DONE (2026-09-15) | internal/experience/candidate.go (RelativeScoreThreshold, ResolveSkillMinScore), internal/analysis/skill.go, app.go (resolveSkillMinScore), SkillReview.svelte |
| LN-24 | ✓ DONE (2026-10-01) | internal/experience/skillusage.go, skillfiles.go (ArchiveSkillFile/RestoreSkillFile), skillquality.go, internal/store (skills columns, ListSkillLoads), app.go |
| LN-25 | ✓ DONE (2026-10-01) | internal/analysis/skillreview.go, internal/experience/skillpilot.go |
| LN-26 | ✓ DONE (2026-10-01) | internal/experience/skillpilot.go (evaluate, trial rules) |
| LN-27 | ✓ DONE (2026-10-01) | internal/session/manager.go (SetSkillAutopilot), app.go, internal/config, SkillReview.svelte |
| LN-28 | ✓ DONE (2026-10-13) | internal/session/handoff_budget.go, session.go, internal/config/types.go |
| LN-29 | ✓ DONE (2026-10-13) | internal/experience/attribution.go, internal/store/store.go, ExperiencePanel.svelte |
| LN-30 | ○ TODO | internal/analysis/*.go (промпт через stdin), cmd/fakeclaude/main.go |
| LN-31 | ○ TODO | internal/experience/skillpilot.go (сбой не сжигает кандидата), internal/analysis/skill.go (пределы промпта) |
| LN-32 | ○ TODO | internal/experience/failures.go, candidate.go (связь по своей команде, Occurrences) |
| LN-33 | ○ TODO | internal/experience/candidate.go, skillpilot.go (read_only_sequence, выбор автопилота), SkillReview.svelte |
| LN-34 | ○ TODO | internal/analysis/skillreview.go, skill.go, internal/experience/skillpilot.go (память отказов) |
| LN-35 | ○ TODO | internal/store (action_signatures.err_text), internal/experience/errtext.go, indexer.go, failures.go |
| LN-36 | ○ TODO | internal/experience/backfill.go, internal/store/store.go, internal/session/manager.go |
| LN-37 | ○ TODO | internal/store (failure_rules), internal/experience/failurerules.go |
| LN-38 | ○ TODO | internal/analysis/failurerule.go, app.go, internal/control/mcptools_failures.go |
| LN-39 | ○ TODO | frontend ExperiencePanel.svelte (вкладка «Ошибки»), stores/experience.ts |
| LN-40 | ○ TODO | internal/experience/skillpilot.go (шаг правил), internal/store (failure_rules.origin/reason), internal/session/session.go, manager.go (LearnedRules) |
| LN-41 | ○ TODO | app.go (RevertFailureRule/RestoreFailureRule), internal/control/mcptools_failures.go, ExperiencePanel.svelte, SkillReview.svelte |

> LN-17 и LN-18 дописаны после разведки корпуса. LN-17 по приоритету идёт **третьим**, сразу за LN-02 (без него нечего анализировать);
> LN-18 — после LN-05, т.к. пишет в тёплый бриф. Порядок берётся из очереди, а не из номеров.

## Инварианты (обязательны для всех задач блока)

1. **Новый пакет `internal/experience/`** — только стандартная библиотека плюс
   уже имеющиеся зависимости. Никаких ML-библиотек, никакой векторной БД.
2. **Всё выключено по умолчанию.** Каждая фича — булев флаг в
   `[optimization]` (глобально) или в `ProjectOverlay`/`SessionConfig`, default
   `false`. Транскрипты содержат код пользователя, поэтому даже read-only
   индексация включается явно (`experience_tracking`).
3. **Приватность.** Дистилляция (скиллы, журнал, хендофф) идёт только через
   Claude (`internal/analysis`), **никогда** через внешних воркеров
   `internal/worker` — там другой privacy-контур (см. `mixed_programming`).
4. **Ничего не пишется в репозиторий пользователя без аппрува.** Скиллы,
   правила разрешений, строки в `CLAUDE.md` — только после явного «Принять» в
   UI. **Исключение (LN-24..27):** скиллы пишет автопилот, но только в проекте,
   где пользователь явно включил `auto_skills` (приватный слой, по умолчанию
   выключено); всё им записанное обратимо (архив, а не удаление) и измеряется. **С LN-40** автопилот при том же `auto_skills` сам применяет и правила из повторяющихся ошибок — не в `CLAUDE.md`, а блоком в `--append-system-prompt` Claude-сессий проекта (в репозиторий ничего не пишется); выключается одним кликом, эффект меряется по повторяемости ошибки; в `CLAUDE.md` правило попадает только по «Принять» (LN-38). Автоматически пишутся лишь файлы под `<project>/.claude-manager/`
   (гитигнорятся через `config.EnsureGitignore`).
5. **Любая фича, меняющая промпт, обязана быть измеримой.** Замер — через
   `session_runs` (LN-11, LN-16). Фича без замера не принимается: молча
   растущий контекст съедает выигрыш, и это не будет видно.
6. **Обратная совместимость промпта.** При выключенных флагах строка,
   отправляемая в CLI, должна быть байт-в-байт прежней — на это пишется тест
   (рядом уже есть образец: `TestUserMessage_Envelope`).
7. **Корень транскриптов конфигурируем** (`CM_TRANSCRIPTS_DIR` или поле
   конфига), иначе задачи невозможно протестировать: `fakeclaude` не пишет в
   `~/.claude/projects/`.

## Что сознательно НЕ делаем

- Векторную БД «всего подряд» и семантический поиск по истории — в таких
  системах не окупается: растит инфраструктуру, а выигрыш даёт та же верхушка
  частотного распределения, которую видно простым счётчиком.
- Автоматическую правку `CLAUDE.md`. (Автоприменение скиллов — только через автопилот LN-24..27 по явному флагу проекта.)
- Обучение на лупах: три и более **идентичных** вызова (`tool` + `arg`) в одном
  прогоне — это дефект (`LoopDetector.Observe` матчит ровно tool+input) или
  симптом потери контекста, а не паттерн. Кандидат обязан встречаться в разных
  прогонах. Внимание: повтор одной *сигнатуры* с разными аргументами — норма,
  отбрасывать по нему нельзя (LN-08).
- Полностью свободную «память агента», которую он пишет сам себе. Пишет
  дистиллятор по фиксированной схеме, объём ограничен, записи протухают.

## Что показал корпус (замеры 2026-09-08)

Разведка проведена по 6207 автосохранённым логам прогонов
(`D:\temp\project-logs-20260908`, 231 МБ, 238 581 запись, три проекта:
lumen-browser 5654 прогона, tbank-pulse-go 548, bankruptcy-platform 5).
Числа ниже — не оценки, а замеры; пороги и правила нормализации в задачах
выведены из них. **Корпус в репозиторий не кладётся**; в `testdata/` уходят
только обрезанные фикстуры.

**1. Чтение файлов — 60-70 % всего контекста.** В lumen `tool_result` суммарно
124.4 M символов (≈31 M токенов по оценке `chars/4`), из них `Read` — 64.7 M
(52 %, 11 136 вызовов, в среднем 5809 символов ≈ 1450 токенов на вызов), плюс
`sed -n` 9.5 M и `grep` 8.4 M. В tbank `Read` — 4.6 M из 7.4 M (62 %). Для
сравнения, сами вызовы инструментов (`tool`) — 117-182 символа. Отсюда
приоритет LN-12 и главный ожидаемый скилл: читать диапазон, а не файл целиком.

**2. Стартовая git-преамбула — кандидат в скиллы №1, подтверждённый.**
tbank: `git remote -v` в 21.7 % прогонов, `git worktree list` 21.4 %,
`git branch -a` 16.1 %, `git branch --show-current` 11.5 %, `git fetch origin`
10.9 %, `git status` 7.8 %. lumen: `git status --short` 10.3 %,
`git pull origin main` 10.0 %, `git branch --show-current` 8.6 %,
`bash scripts/worktree-pool.sh …` 8.6 % + 7.7 %. Это ровно протокол
session-start из `DefaultP1SessionPrompt`, который агент каждый раз собирает
заново из 5-6 вызовов. Причём `git branch -a` в tbank — второй по объёму
потребитель контекста после `Read` (2903 символа в среднем: там сотни
worktree-веток).

**3. Ошибки повторяются и лечатся одной строкой факта.** tbank: 73 раза
`fatal: 'origin' does not appear to be a git repository` (13 % прогонов),
13+10 раз `fatal: ambiguous argument 'main'` (ветка называется `master`),
9 раз `cannot remove a locked working tree`. lumen: 197 раз
`File content exceeds maximum allowed tokens. Use offset and limit` (плюс 42
раза по размеру), 187 раз `Command timed out`, 69 раз `cd: .claude…: No such
file or directory`, 38 раз `File has not been read yet. Read it first`. Это
вход LN-07 и главный аргумент за уровень «факт в CLAUDE.md» вместо скилла.

**4. Наивная маскировка литералов уничтожает смысл.** Первая версия
нормализатора выдала топ `Bash:git <ARG> -v` и `Bash:git <ARG> <ARG>` —
`git status` и `git push` склеились в одну сигнатуру. Субкоманду маскировать
нельзя (см. LN-02).

**5. Навигация забивает топ.** До среза префиксных `cd`/`export` первой
сигнатурой шла `Bash:cd <ARG>`: 20 388 вызовов в 737 прогонах — артефакт того,
что каждый Bash-вызов на Windows начинается с `cd "<project>"`. После среза
она исчезает из топ-25 полностью.

**6. Абсолютные пути в сигнатуре бесполезны.** `Read:D:/RustProjects/*.md` —
первые два сегмента это префикс машины. Путь нормализуется относительно
`ProjectPath` до вычисления сигнатуры.

**7. Повтор сигнатуры внутри прогона — норма, а не луп.** У 85 % топовых
сигнатур `loopruns > 0`; `Read` разных файлов, естественно, повторяется.
Настоящий повтор — идентичные `tool` + `arg`: `Read` 16.5 % прогонов, `Edit`
14.3 %, `Bash` 11.8 %. И это не паттерн для скилла, а **симптом потери
контекста** (перечитывание того же файла) — такие случаи идут во вход LN-15 и
LN-05, а не в дистиллятор.

**8. Абсолютный порог `min_runs` не работает на большом проекте.**
`min_runs = 3` при 5654 прогонах даёт 719 сигнатур — на два порядка больше,
чем имеет смысл дистиллировать. Порог должен быть относительным (доля
прогонов); 5 % даёт ~30-40 кандидатов в lumen и ~15 в tbank.

**9. Результат вызова не всегда идёт следом за вызовом — 87.4 %.** Ломают
параллельные вызовы (до 11 записей `tool` подряд), хартбиты `tool_progress`
и провалы, приходящие уровнем `error`. Наивная привязка «следующая запись»
теряет ровно длинные вызовы: с ней максимум длительности по всему корпусу
выходит 109 с, с правильной (очередь FIFO) — 5422 с. Правило зафиксировано
в LN-17.

**10. Длительности команд — на порядок разные между проектами.** lumen: 3465
вызовов дольше минуты, 1806 дольше двух, 530 дольше пяти; `bash
scoped-test.sh` — медиана 155-399 с при p90 на потолке 602 с; `git worktree
add -b` — медиана 120 с и 31 провал из 90; `sleep` — 953 вызова с медианой
108 с (≈28 часов ожидания суммарно). tbank: 6 вызовов дольше двух минут.
Отсюда LN-18 и требование делать профиль на проект.

**11. Хартбиты `tool_progress` — 9.2 % записей лога lumen** (20 399 записей,
6.1 M символов). Парсер приложения их не разбирает, поэтому сырой JSON
оседает в логе как запись уровня `system`. Дефект вне этого блока (чинится в
`internal/session/parser.go`), но ingest обязан этот шум фильтровать.

---

## LN-01: Чтение транскриптов Claude CLI

**Depends on:** ничего
**Files:** `internal/experience/transcript.go`, `internal/experience/transcript_test.go`,
`testdata/transcripts/*.jsonl`

Транскрипт CLI — самый богатый источник: есть `tool_use`, его результат и usage
на шаг. `session_logs` хранит только вызовы (`tool_name`/`tool_input`), без
результатов.

**`Trajectory` — общая абстракция, а не формат.** Второй бэкенд (разбор
автосохранённых markdown-логов, LN-17) отдаёт тот же тип; всё, что выше по
стеку, обязано работать с обоими и не заглядывать в формат. Поля, которых нет
в markdown-бэкенде (`Usage`, `ToolUseID`), там просто нулевые — потребители не
должны на них падать.

**Где лежит:** `~/.claude/projects/<slug>/<cli-session-id>.jsonl`, где `slug` —
путь проекта, в котором `:` и разделители пути заменены на `-`
(проверено: `D:\GolangProjects\claude-manager` → `D--GolangProjects-claude-manager`).
Не полагаться на slug как на единственный способ: если файла нет, сканировать
все подпапки `~/.claude/projects/` в поисках `<cli-session-id>.jsonl`. Корень
берётся из `CM_TRANSCRIPTS_DIR`, если задан.

**Формат (проверено на реальном файле, 351 КБ):** JSONL, у каждой строки есть
`type`. Значимы `assistant` и `user` (у обоих `message.content` — массив
блоков), остальные (`attachment`, `system`, `mode`, `last-prompt`,
`permission-mode`, `atis-latch`, `ai-title`, `file-history-snapshot`,
`file-history-delta`, `queue-operation`) игнорировать молча и **не падать на
неизвестных** — набор типов меняется между версиями CLI. Блоки: у `assistant` —
`tool_use` / `text` / `thinking`, у `user` — `tool_result`. Результат
инструмента: блок `{tool_use_id, content, is_error}` плюс сайдкар на уровне
строки — `toolUseResult` с ключами `stdout`, `stderr`, `interrupted`,
`isImage`. Строка также несёт `sessionId`, `cwd`, `gitBranch`, `timestamp`,
`uuid`/`parentUuid`.

**Типы:**

```go
type Trajectory struct { SessionID, ProjectPath, Branch string; Steps []Step; Skipped int }
type Step struct {
    Index int; Time time.Time
    Kind  StepKind // tool_use | text | thinking
    ToolName  string
    Input     json.RawMessage // сырой JSON вызова; пусто у markdown-бэкенда
    InputText string          // «говорящее» поле вызова одной строкой
    ToolUseID string
    ResultText string; ResultChars int; ResultIsError bool
    Stdout, Stderr string
    Usage *session.TokenUsage
}
```

**`InputText` — обязательное поле, и заполняет его именно этот бэкенд.** Это
та же величина, что уже кладётся в `session_logs.tool_input`: команда для
`Bash`, путь для `Read`/`Edit`/`Write`, паттерн для `Grep`/`Glob` — правила
выделения см. `abbreviateInput` в `internal/session/parser.go` (вынести общую
функцию, а не дублировать). Всё, что выше по стеку, работает с `InputText`, а
не с `Input`: у markdown-бэкенда (LN-17) сырого JSON нет вообще.

Склейка `tool_use` ↔ `tool_result` по `tool_use_id` (результат пишется в тот же
`Step`, отдельного шага для результата не заводить — иначе индексы поедут).

**Инкрементальность:** `ReadFrom(path string, offset int64) (Trajectory, int64, error)` —
дочитывает только хвост файла; **неполная последняя строка не потребляется**
(offset не двигается за неё), потому что CLI пишет в файл параллельно с
индексацией.

**Устойчивость:** битая строка → пропуск и `Skipped++`, не ошибка.

**Готово когда:** тесты на фикстуре (взять реальный транскрипт, обрезать до
~50 строк, вычистить пути): склейка tool_use/tool_result, игнор неизвестных
типов, повторное чтение с offset не дублирует шаги, неполная строка не теряется
при дочитывании, битая строка не роняет парсер. Ноль новых зависимостей.

## LN-02: Сигнатуры действий и схема БД

**Depends on:** LN-01
**Files:** `internal/experience/signature.go`, `signature_test.go`,
`internal/store/migrations.go`, `internal/store/store.go`, `internal/store/store_test.go`

Сырой вызов инструмента не агрегируется (`sed -n 320,370p parser.go` встречается
ровно один раз в жизни). Агрегируется **сигнатура** — форма действия с
замаскированными литералами.

`Signature(tool, inputText, projectPath string) (sig string, arg string)` —
на вход идёт `Step.InputText` (не сырой JSON: у markdown-бэкенда его нет),
`arg` хранит исходное значение целиком, `sig` нормализован:

| Инструмент | `sig` | Пример |
|---|---|---|
| `Bash` | argv-форма первого **содержательного** сегмента, литералы → `<ARG>`, максимум 6 токенов | `Bash:cargo build -p <ARG> --profile <ARG>` |
| `Read`/`Edit`/`Write` | путь относительно проекта: первые два сегмента + расширение | `Read:internal/experience/*.go` |
| `Grep`/`Glob` | паттерн как есть (он и есть смысл вызова), обрезанный до 40 символов | `Grep:func \(s \*Store\)` |
| прочие | имя инструмента + первый токен `InputText`, до 30 символов | `WebFetch:https://…` |

Пять правил ниже выведены из замеров (пункты 4-6 раздела «Что показал
корпус»); без любого из них топ вырождается в мусор:

1. **Субкоманду не маскировать.** Для мультикомандных утилит (`git`, `go`,
   `cargo`, `npm`, `docker`, `gh`, `kubectl`, `pip`, `python`, `wails`) первый
   неопционный токен, не похожий на литерал, сохраняется дословно. Иначе
   `git status` и `git push` дают одну сигнатуру `git <ARG>` — проверено, это
   ровно то, что произошло на первом прогоне разведки.
2. **Навигационный префикс срезать.** Из цепочки сегментов (`&&`, `||`, `;`,
   `|`) берётся первый, чья команда не входит в `{cd, export, pwd, source,
   set}`. Без этого `Bash:cd <ARG>` — сигнатура №1 с 20 388 вызовами.
3. **Путь — относительно `ProjectPath`** (и только если он внутри проекта;
   иначе — как есть). Абсолютный префикс машины в сигнатуре бесполезен.
4. **Маскировать** числа, токены с `/`, `\` или `.`, кавычные строки,
   `$`-подстановки, хеши, UUID. **Не маскировать** флаги (`-p`, `--release`,
   `--no-ff`) — они и отличают действие; `--flag=value` усекается до `--flag`.
5. **У интерпретаторов имя скрипта — это субкоманда.** Для `bash`, `sh`,
   `python`, `node` первый позиционный аргумент маскируется до базового имени
   файла, а не до `<ARG>`: `bash scripts/worktree-pool.sh p3-work` →
   `Bash:bash worktree-pool.sh <ARG>`. В корпусе это 8.6 % прогонов lumen, и
   без правила все запуски скриптов сливаются в одну бессмысленную сигнатуру.

Таблица ожидаемых значений для тестов (реальные вызовы из корпуса):

| Вход | `sig` |
|---|---|
| `cd "D:/RustProjects/lumen-browser" && git status --short` | `Bash:git status --short` |
| `git merge --no-ff p3-invest-hydration -m "…"` | `Bash:git merge --no-ff <ARG> -m <ARG>` |
| `cargo clippy -p lumen-network --all-targets -- -D warnings` | `Bash:cargo clippy -p <ARG> --all-targets --` |
| `bash scripts/worktree-pool.sh p3-work p3-bug-756 2>&1` | `Bash:bash worktree-pool.sh <ARG> <ARG> <ARG>` |
| `sed -n '80,220p' scripts/scroll_perf.py` | `Bash:sed -n <ARG> <ARG>` |

**Схема** (идемпотентные `CREATE TABLE IF NOT EXISTS` в тот же `migrate()`;
таблицы версий миграций в проекте нет):

```sql
CREATE TABLE IF NOT EXISTS action_signatures (
    id             INTEGER PRIMARY KEY,
    project        TEXT NOT NULL,
    session        TEXT NOT NULL,
    run_id         INTEGER REFERENCES session_runs(id),
    cli_session_id TEXT,
    task_ptr       TEXT,
    step_index     INTEGER NOT NULL,
    tool           TEXT NOT NULL,
    sig            TEXT NOT NULL,
    arg            TEXT,
    is_error       INTEGER DEFAULT 0,
    out_tokens     INTEGER DEFAULT 0,   -- output_tokens шага; 0 у md-бэкенда (LN-17)
    result_chars   INTEGER DEFAULT 0,   -- размер tool_result: цена переоткрытия
                                        --   (LN-08) и атрибуция (LN-12)
    ts             DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS ingest_state (
    cli_session_id TEXT PRIMARY KEY,
    path           TEXT NOT NULL,
    offset         INTEGER NOT NULL,
    updated_at     DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sig_project ON action_signatures(project, sig);
CREATE INDEX IF NOT EXISTS idx_sig_run     ON action_signatures(run_id);
```

**Методы Store:** `InsertActions(rows []ActionRow) error` (батч в одной
транзакции, как `InsertLogs`), `TopSignatures(project string, sinceDays, limit int) ([]SignatureStat, error)`
(поля: `Sig`, `Tool`, `Count`, `DistinctRuns`, `ErrorRate`, `SumOutTokens`,
`SampleArgs []string`, `FirstSeen`, `LastSeen`), `ActionsForRun(runID int64)`,
`GetIngestOffset` / `SetIngestOffset`.

**Готово когда:** табличные тесты сигнатур (≥15 кейсов, включая пайплайны,
кавычки, windows-пути), тесты Store на временной БД, тест что повторная
миграция на существующей БД не ломается.

## LN-03: Индексация прогонов + вкладка «Actions»

**Depends on:** LN-02
**Files:** `internal/experience/indexer.go`, `indexer_test.go`,
`internal/session/manager.go`, `app.go`, `internal/config/types.go`,
`config.example.toml`, `frontend/src/components/ExperiencePanel.svelte`,
`frontend/src/stores/experience.ts`, `frontend/tests/experience.spec.ts`

**Флаг:** `[optimization] experience_tracking = false` (default). Выключен —
транскрипты не открываются вообще.

**Точка врезки:** `SessionManager.finishRun`, сразу после `InsertLogs`, в
отдельной горутине с `logger.Recover` — тем же паттерном, что автосохранение
лога рядом (сбой индексации не должен влиять на статус прогона, только на лог).
Индексатору нужны `project`, `session`, `runID`, `ms.session.CLISessionID`,
`ms.session.ProjectPath` и текущий `taskSourceDesc` (→ `task_ptr`).

`Indexer.IngestRun(...)`: найти транскрипт (LN-01) → дочитать с сохранённого
offset → отфильтровать шаги `tool_use` → сигнатуры (LN-02) → `InsertActions` →
`SetIngestOffset`. Идемпотентность обеспечивается offset'ом.

**Импорт чужой истории — не здесь.** Массовый импорт каталога (`IngestDir`,
строки с `run_id = NULL`) — задача LN-17; тут только требование к запросам:
всё, что опирается на `run_id`, обязано корректно работать при `NULL`
(группировка по `cli_session_id`, если `run_id` пуст), иначе импортированный
корпус не будет виден в «Actions».

**Биндинги:** `GetTopActions(project string, days int) ([]store.SignatureStat, error)`,
`GetActionSamples(project, sig string, limit int) ([]store.ActionRow, error)`.

**UI:** кнопка «Experience» в хедере `App.svelte` → модал
`ExperiencePanel.svelte` с вкладками (в этой задаче одна — «Actions»: таблица
сигнатура / N / прогонов / ошибок % / токены, сортировка по колонкам, выбор
проекта и периода). Последующие задачи блока добавляют вкладки в этот же модал,
новых модалов не заводить.

**Готово когда:** unit-тест индексатора на фикстуре транскрипта +
`CM_TRANSCRIPTS_DIR`; тест что строка с `run_id = NULL` попадает в
`GetTopActions`; Playwright:
открыть панель, увидеть строки, кликнуть сигнатуру → примеры; тест что при
`experience_tracking=false` файлы не читаются.

## LN-04: Предложения правил разрешений

**Depends on:** LN-03
**Files:** `internal/experience/allowlist.go`, `allowlist_test.go`,
`internal/store/migrations.go` (`permission_events`), `internal/session/manager.go`,
`app.go`, `ExperiencePanel.svelte` (вкладка «Permissions»)

Самый дешёвый выигрыш: команда, которую агент выполняет в каждом прогоне и на
которой каждый раз встаёт `waiting_permission`.

**Сбор:** таблица `permission_events(id, project, session, run_id, tool, pattern,
decision, auto INTEGER, ts)`, пишется из `SessionManager.handlePermission` — и
авто-решения по правилам, и решения человека (нужно и то и другое, чтобы видеть,
какое правило уже покрывает случай).

**Предложение** — `PermissionRule{Tool, Pattern, Decision:"allow"}` в overlay
проекта (`<project>/.claude-manager/config.toml`), т.е. в **собственный** слой
правил менеджера (`internal/permission/rules.go`), который срабатывает раньше
UI. Дополнительно — кнопка «Экспортировать в `.claude/settings.json`» для
запусков CLI вне менеджера; это отдельный, необязательный выход.

**Классификатор безопасности (жёсткий, whitelist-подход):** предлагать к
авторазрешению только read-only действия — `Read`, `Grep`, `Glob` и Bash-команды
из явного списка (`ls`, `cat`, `head`, `tail`, `sed -n`, `grep`, `rg`, `find`,
`git status|log|diff|show`, `go build|test|vet`, `cargo check|build|test|clippy`,
`npm test|run build`). Всё, где встречается `rm`, `mv`, `>`/`>>`, `curl`, `wget`,
`ssh`, `git push|reset|checkout --`, `--force`, `sudo`, — не предлагать никогда,
даже при высокой частоте; показывать отдельным списком «требует ручного
решения». Тесты обязаны прямо проверять, что `rm -rf` не попадает в предложения.

**Применение:** кнопка «Добавить правило» → `GetConfig` → мутация → `UpdateConfig`
(тот же round-trip, что везде).

**Готово когда:** тесты классификатора (≥20 кейсов, безопасное/опасное), тест
записи правила в overlay, Playwright на список предложений и кнопку.

## LN-05: Тёплый бриф (context primer)

**Depends on:** LN-03
**Files:** `internal/experience/primer.go`, `primer_test.go`,
`internal/session/session.go`, `internal/config/types.go`, `config.example.toml`

Главный слив токенов в `auto_restart`-цикле: каждая свежая сессия заново
открывает репозиторий, хотя менеджер уже знает состояние.

**Флаг:** `SessionConfig.ContextPrimer bool` (`context_primer`, default false).

`BuildPrimer(in PrimerInput) string` собирает блок ≤2000 символов (жёсткий
потолок, урезание по секциям в фиксированном порядке приоритета):

1. Текущая задача — `task_source` + разрешённое описание (`taskSourceDesc`).
2. Git-состояние: **имя основной ветки, наличие и имя remote**, текущая
   ветка/worktree, `git log --oneline -3` (прямой `exec` с таймаутом 3 с;
   ошибка → секция просто отсутствует, старт не блокируется). Remote и имя
   основной ветки — не украшение: в корпусе tbank 73 прогона (13 %) упёрлись в
   `fatal: 'origin' does not appear to be a git repository` и 23 — в
   `ambiguous argument 'main'` при ветке `master`. Две строки в примере
   убирают обе ошибки.
3. Файлы, изменённые предыдущим прогоном этой сессии: из `action_signatures`
   по последнему `run_id` — `arg` шагов `Edit`/`Write`; для plan-driven
   дополнительно `plan_subtasks.files_changed`.
4. Команды ворот проекта (`ProjectConfig.Gates`) — чем проверяется работа.
5. (после LN-08) файлы, которые прошлый прогон перечитывал 3+ раз
   (`ContextLossSuspect`) — сигнал «это понадобится снова».
6. (после LN-06) до трёх последних строк `avoid` из журнала.

**Врезка:** `Session.initialPromptText`, только когда `!recovering &&
!forceInteractive`, префиксом к `Config.Prompt` с явным разделителем
(`--- Project state (auto-generated) ---`). Ветку crash-recovery **не трогать** —
у неё своя семантика.

**Готово когда:** golden-тест текста на фиксированном входе; тест потолка длины
и порядка урезания; тест, что при `context_primer=false` промпт байт-в-байт
прежний; тест, что падение `git` не ломает старт.

## LN-06: Журнал проекта

**Depends on:** LN-05
**Files:** `internal/experience/journal.go`, `journal_test.go`,
`internal/analysis/journal.go`, `internal/analysis/schema.go`,
`internal/session/manager.go`, `internal/config/types.go`

Эпизодическая память между сессиями: одна короткая запись на завершённую задачу.

**Флаг:** `ProjectOverlay.Journal bool` (`journal`, default false, приватный
слой `config.local.toml`).

**Триггер:** `finishRun` со `status == "completed"`, горутина с `logger.Recover`.

**Дистилляция:** `analysis.RunAnalysis` (haiku — дёшево, вызывается часто) с
новой схемой `JournalJSONSchema`: `{done: string, surprises: []string, avoid:
[]string}`. Вход — указатель задачи, изменённые файлы, финальный `result`-текст
прогона, вывод упавших ворот (если были). Инъекция функции анализа для тестов —
по образцу `analyzeFn` / `roadmapAnalyzeFn` в `manager.go`.

**Формат файла** `<project>/.claude-manager/journal.md` — append одной секции:

```markdown
## 2026-09-08 — ROADMAP.md:92
**Сделано:** …
**Неожиданно:** …
**Не делать:** …
```

Запись атомарная (tmp → rename), файл гитигнорится (`config.EnsureGitignore`);
`journal_commit = true` отменяет гитигнор для команд, которые хотят его
коммитить. Ротация: держать 50 последних секций, старшее уезжает в
`journal-archive-YYYY-MM.md`.

**Чтение:** `LastEntries(n int) []Entry` — используется LN-05.

**Готово когда:** тесты append/ротации/атомарности, тест с заглушкой
дистиллятора, тест что при выключенном флаге ничего не пишется и Claude не
вызывается.

## LN-07: Промоушен провалов в правила

**Depends on:** LN-03
**Files:** `internal/experience/failures.go`, `failures_test.go`

Пара «упало → следующим шагом починили» — сигнал качеством выше любой частотной
статистики: это ровно то знание, которого не хватало сессии.

**Источник A — раунды mixed-программирования.** `worker.MixedTask.Rounds`: пара
(раунд N с `Gates.Passed == false`, раунд N+1 с `Passed == true`). Из первого —
`Gates.FailedCommand()` (команда + вывод), из второго — применённые патчи.
Читать через существующий `worker.TaskStore` / `GetMixedRounds`.

**Источник B — шаги сессий.** В `action_signatures`: строка с `is_error = 1`, за
которой в пределах 5 шагов того же прогона идёт та же `sig` с `is_error = 0`
(агент подобрал флаг/окружение). Хранить обе `arg`-строки — разница между ними и
есть правило.

**Агрегация:** нормализовать первую строку ошибки (выкинуть пути, адреса,
тайминги, номера строк) и группировать по ней; кластер = `{ErrorKey, Count,
DistinctRuns, Examples []FixPair}`.

**Выход:** `[]FailureCluster`, отсортированный по `DistinctRuns`. Потребители —
LN-09 (высокоприоритетный вход дистиллятора) и вкладка «Failures» в
`ExperiencePanel` (только просмотр).

**Нормализация ключа ошибки** (проверено на корпусе): вычистить абсолютные пути
(`[A-Za-z]:[\\/]…` и `/…`), заменить все числа на `N`, обрезать до 80 символов.
Без этого один и тот же `fatal:` с разными путями рассыпается на десятки
кластеров.

**Обязательные тест-кейсы — реальные кластеры из корпуса** (см. «Что показал
корпус», п. 3). Каждый должен схлопнуться в один кластер и дать осмысленное
правило:

| Кластер | Замер | Ожидаемое правило |
|---|---|---|
| `fatal: 'origin' does not appear to be a git repository` | 73 раза, 13 % прогонов tbank | факт в CLAUDE.md: remote `origin` не настроен |
| `fatal: ambiguous argument 'main': unknown revision` | 23 раза | факт: основная ветка `master`, не `main` |
| `File content (N tokens) exceeds maximum allowed tokens. Use offset and limit` | 197 раз + 42 по размеру, lumen | скилл: читать диапазоном, не файл целиком |
| `File has not been read yet. Read it first before writing to it` | 38 раз | правило порядка Read → Edit |
| `cannot remove a locked working tree, lock reason: claude session` | 9 раз | правило снятия lock перед удалением worktree |

**Готово когда:** тесты извлечения пар из фикстуры `MixedTask` JSON и из
синтетических строк сигнатур; тест нормализации ключа ошибки на пяти кластерах
из таблицы выше (в том числе: две записи с разными путями → один кластер).

## LN-08: Кластеризация и скоринг кандидатов

**Depends on:** LN-07
**Files:** `internal/experience/candidate.go`, `candidate_test.go`

Отбирает то, из чего вообще имеет смысл делать скилл. Наивный n-gram-майнинг
даёт мусор — правила ниже нужны целиком.

**Кандидат** = одиночная сигнатура или упорядоченная n-грамма (2..4) сигнатур
в пределах одного прогона, встречающаяся в **≥ `min_run_share` (по умолчанию
5 %) прогонов проекта**, но не менее чем в 3 прогонах. Доля, а не абсолютное
число: замер показал, что `min_runs = 3` при 5654 прогонах даёт 719 сигнатур —
на два порядка больше, чем имеет смысл дистиллировать; 5 % дают ~30-40
кандидатов в lumen и ~15 в tbank («Что показал корпус», п. 8).

**Отсев лупов — по `arg`, а не по сигнатуре.** Повтор одной *сигнатуры* внутри
прогона это норма (`Read` разных файлов; у 85 % топовых сигнатур такой повтор
есть) — отбрасывать по нему нельзя, иначе выкинется весь топ. Луп — это три и
более **идентичных** `tool` + `arg` в одном прогоне (`optimization.LoopDetector`
матчит именно tool+input). Такие прогоны засчитываются за один и помечаются
`ContextLossSuspect`: в корпусе это `Read` 16.5 %, `Edit` 14.3 %, `Bash` 11.8 %
прогонов, и это симптом потери контекста (перечитывание того же файла), а не
паттерн. Отдельным выходом отдаются в LN-05 и LN-15, в дистиллятор не идут.

**Дедупликация:** n-грамма не выдаётся, если её `DistinctRuns` равен таковому у
более длинной n-граммы, которая её содержит — выигрывает максимальная.

**Скор:**

```
score = distinctRuns * log(1+rediscoveryChars) * outcomeWeight
```

- `rediscoveryChars` — сумма `result_chars` шагов от начала задачи до первого
  вхождения последовательности: объём вывода, который модель прочитывает, чтобы
  каждый раз заново дойти до этого действия. Считается по `result_chars`, а не
  по `out_tokens`, ровно по двум причинам: это настоящая статья расхода
  (`tool_result` — 60-70 % контекста, «Что показал корпус», п. 1) и это
  единственное, что доступно в обоих бэкендах ingest (в markdown-логах usage на
  шаг отсутствует).
- `outcomeWeight` — `1.0` для прогонов со `status='completed'`, `0.3` для
  `stopped`, `0.0` для `error`. Учиться на провалившихся прогонах нельзя. При
  импортированном корпусе без `run_id` статус неизвестен — вес `0.6`
  (нейтральный), и это фиксируется флагом `Imported` в кандидате.

**Выход:** `Candidate{Sig []string, DistinctRuns int, RunShare float64, Score
float64, Samples []store.ActionRow, RelatedFailures []FailureCluster,
ContextLossSuspect bool, Imported bool, FirstSeen, LastSeen}`.

**Ожидаемый результат на корпусе** (используется как приёмочный сценарий, не
как unit-тест): в топе tbank обязана оказаться стартовая git-преамбула
(`git remote -v` 21.7 %, `git worktree list` 21.4 %, `git branch -a` 16.1 %,
`git branch --show-current` 11.5 %, `git fetch origin` 10.9 %) — это самый
дорогой повторяющийся ритуал в корпусе и первый кандидат в скиллы.

**Готово когда:** табличные тесты на синтетическом наборе строк; явный тест
«три идентичных `tool`+`arg` в одном прогоне не поднимают кандидата, а ставят
`ContextLossSuspect`»; тест что повтор одной сигнатуры с разными `arg` кандидата
НЕ отбрасывает; тест дедупликации вложенных n-грамм; тест что прогон со
`status='error'` не тянет кандидата вверх; тест относительного порога на двух
проектах разного размера.

## LN-09: Дистиллятор скиллов

**Depends on:** LN-08
**Files:** `internal/analysis/skill.go`, `internal/analysis/schema.go`,
`internal/analysis/skill_test.go`, `internal/store/migrations.go`,
`internal/session/manager.go`, `app.go`

Превращает кандидата в черновик SKILL.md. Дистиллирует модель, а не regex: из
последовательности команд нужно восстановить намерение, условие применения и
критерий готовности.

**Вход промпта:** сигнатуры кандидата, до 5 реальных примеров (команда + первые
20 строк её вывода, обрезанные), связанные кластеры провалов (LN-07), список
ворот проекта.

**`SkillJSONSchema`:**

```json
{"name":"kebab-case","description":"одно предложение ≤200 символов: КОГДА применять",
 "when_to_use":["…"],"steps":[{"command":"…","why":"…"}],
 "gotchas":["…"],"done_when":"…","files_touched":["…"]}
```

`description` — самая важная строка: только она постоянно висит в контексте, по
ней модель решает, грузить ли тело. Это оговаривается прямо в системном промпте.

**Прогон:** `RunAnalysisStreaming` (по умолчанию sonnet, модель конфигурируема),
прогресс — событие `skill:progress` по образцу `plan:roadmap_progress`.

**Рендер:** `RenderSkillMarkdown(SkillDraft) string` → YAML-фронтматтер
(`name`, `description`) + тело ≤120 строк.

**Персист:**

```sql
CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY, project TEXT NOT NULL, name TEXT NOT NULL,
    status TEXT NOT NULL,           -- draft | approved | archived
    draft_json TEXT NOT NULL, md TEXT NOT NULL, source_json TEXT NOT NULL,
    created_at DATETIME NOT NULL, approved_at DATETIME, archived_at DATETIME
);
```

`source_json` — сигнатуры кандидата; по ним LN-11 определяет, сработал ли скилл.

**Порог:** дистиллятор не вызывается для кандидата со `score` ниже
настраиваемого порога — это платный вызов модели.

**Готово когда:** тест декодирования схемы, golden-тест рендера markdown, тест
порога, тест что при недоступном Claude возвращается ошибка, а не паника.

## LN-10: Аппрув и запись скилла

**Depends on:** LN-09
**Files:** `frontend/src/components/SkillReview.svelte`, `ExperiencePanel.svelte`
(вкладка «Skills»), `internal/experience/skillfiles.go`, `skillfiles_test.go`,
`app.go`, `frontend/tests/skills.spec.ts`

**UI:** список черновиков (имя, description, score, из скольких прогонов),
просмотр/редактирование markdown в стиле `PlanReview.svelte` (рендер через общий
`renderMarkdown` + `.md-body`), кнопки «Принять» и «В архив».

**Запись:** `<project>/.claude/skills/<name>/SKILL.md`, атомарно (tmp → rename).
Файл **не** гитигнорится — скиллы предназначены для коммита и шаринга. Если файл
уже существует — inline-баннер «уже есть — перезаписать?» (никакого
`window.confirm()`, он в WebView2 всегда `false`). Путь обязан проходить проверку
на выход за пределы папки проекта (образец — `confinedPath` в
`internal/analysis/roadmapview.go`), имя — только `[a-z0-9-]`.

**Биндинги:** `GetSkills(project string)`, `ApproveSkill(id int64, md string,
overwrite bool) (path string, err error)`, `ArchiveSkill(id int64)`.

Автоматическая запись запрещена инвариантом 4 — скилл появляется в репозитории
только после клика.

**Готово когда:** Playwright: черновик → правка → «Принять» → файл создан
(проверка через control-plane), повторный аппрув даёт баннер, «В архив» убирает
из списка; unit-тест на отказ при `name = "../evil"`.

## LN-11: Замер эффекта скиллов и протухание

**Depends on:** LN-10
**Files:** `internal/experience/skillquality.go`, `skillquality_test.go`,
`ExperiencePanel.svelte` (таблица), `app.go`

Без этого библиотека скиллов растёт, контекст дорожает, и никто не знает,
помогло ли. Структура отчёта — по образцу `worker/quality.go` `ModelQuality`.

**`SkillEffect`** на скилл: прогоны **до** `approved_at` и **после**, в том же
проекте, отфильтрованные до сопоставимых — те, среди шагов которых есть хотя бы
одна сигнатура из `source_json`. По каждой стороне: медиана `input_tokens`,
медиана `num_turns`, доля `status='completed'`, число прогонов.

Медиана, не среднее: распределение стоимости прогонов длиннохвостое.

**Импортированные прогоны в замер не входят.** У строк с `run_id = NULL`
(корпус с другой машины, LN-17) нет записи в `session_runs`, а значит нет ни
токенов, ни статуса. Они годятся, чтобы найти кандидата (LN-08), но не чтобы
измерить эффект — иначе «до» будет пустым и любой скилл покажет улучшение.
Фильтровать явно, а не полагаться на `JOIN`.

**Явная пометка «данных мало»** при <3 прогонах с любой стороны — и никаких
выводов в UI по таким строкам.

**Протухание** (только предложение, не автодействие): скилл, у которого ни одна
сигнатура не встретилась за последние 20 прогонов проекта, или у которого при
≥5 прогонах после аппрува медиана токенов не уменьшилась, помечается «предложить
в архив».

**Готово когда:** тесты на синтетических наборах прогонов (улучшение, ухудшение,
мало данных, скилл ни разу не сработал); Playwright на таблицу.

## LN-12: Атрибуция токенов по инструментам

**Depends on:** LN-03
**Files:** `internal/experience/attribution.go`, `attribution_test.go`,
`app.go`, `ExperiencePanel.svelte` (вкладка «Cost by tool»)

Сейчас видно «сколько сожгла сессия» и не видно «какой вызов съел контекст».
Замер по корпусу говорит, что это не диагностическая мелочь, а главная статья
расхода: в lumen `Read` даёт 64.7 M символов из 124.4 M всего вывода
инструментов (52 %, 11 136 вызовов, в среднем 5809 символов ≈ 1450 токенов на
вызов), в tbank — 62 %. Следом идут `sed -n` (9.5 M) и `grep` (8.4 M). При этом
197 раз в корпусе встречается отказ «File content exceeds maximum allowed
tokens. Use offset and limit» — то есть модель регулярно читает файл целиком,
получает отказ и читает заново. Это и есть первый обоснованный скилл.

`EstimateTokens(chars int) int` — приближение `chars/4`; константа вынесена и
задокументирована как оценка (точного счётчика без API нет, здесь важен порядок,
а не точность).

**Отчёт:** топ-N самых дорогих вызовов за период (`result_chars` пишется LN-02):
сигнатура, суммарные оценочные токены, доля от общего, средний размер одного
результата, максимум. Второй срез — по инструменту.

Питает `rediscoveryChars` в LN-08 и даёт материал для LN-16.

**Готово когда:** тесты оценщика и агрегатора; вкладка показывает данные; тест
что вызовы без результата (`result_chars = 0`) не ломают проценты.

## LN-13: Роутинг моделей по измеренному исходу

**Depends on:** LN-03
**Files:** `internal/optimization/routing.go`, `internal/optimization/outcomes.go`,
`outcomes_test.go`, `internal/store/store.go`

Сейчас `ModelRouter.Route()` — статическая таблица «сложность → модель». Замкнуть
петлю: у проекта уже есть исходы.

**Агрегат** (запрос, не таблица — считается из `session_runs` + `task_plans`,
откуда берётся `estimated_complexity` из `analysis_json`):
`OutcomeStats{Project, Complexity, Model, Effort, Runs, Completed, AvgCost, AvgTurns}`.

**Правило:** `Route()` принимает опциональный `OutcomeProvider`. Если для
(project, complexity) набрано ≥5 прогонов и модель уровнем ниже даёт ≥90 %
`completed` при меньшей средней стоимости — рекомендуется она, а
`ModelRecommendation.Reason` заполняется человекочитаемо («на основании 7
прогонов: sonnet 7/7 completed, дешевле opus в 4.1×»), чтобы решение было видно
в `ModelPicker`.

**Ограничения:** никогда не понижать модель для `architectural`; никогда не
менять решение молча — только через видимую в UI рекомендацию, которую человек
может переопределить (механика `StartSessionWithModel` уже есть).

**Готово когда:** табличные тесты с провайдером-заглушкой; тест что без данных
(провайдер `nil` или мало прогонов) рекомендации байт-в-байт прежние.

## LN-14: Кэш-ориентированный порядок запуска

**Depends on:** LN-03
**Files:** `internal/optimization/cache.go`, `internal/experience/affinity.go`,
`affinity_test.go`, `frontend/src/components/CostDashboard.svelte`

`CacheTracker.StartProjectOptimized` сейчас только разносит старты во времени
(`session_start_delay`). Добавить порядок: сначала группировать сессии по модели
(переключение модели обнуляет общий префикс), внутри группы — по убыванию
пересечения файлов из последнего прогона (`action_signatures`, пути
`Read`/`Edit`/`Write`).

**Измерение:** доля `cache_read_tokens` от суммы входных токенов по проекту, до
и после — вывести в `CostDashboard` рядом с существующей cache efficiency.

**Готово когда:** детерминированный тест сортировки (фиксированный вход →
ожидаемый порядок); тест что при отсутствии истории порядок совпадает с текущим
(инвариант 6).

## LN-15: Хендофф вместо `--resume` при переполнении контекста

**Depends on:** LN-06
**Files:** `internal/experience/handoff.go`, `handoff_test.go`,
`internal/optimization/context.go`, `internal/session/session.go`,
`internal/config/types.go`, `testdata/scenarios/`, `testdata/e2e/`

`ContextMonitor` рестартует сессию при >75 % окна, и рестарт через `--resume`
тащит за собой ровно тот дорогой префикс, из-за которого рестарт и случился.
Компактный хендофф — порядка тысячи токенов вместо десятков тысяч.

**Флаг:** `SessionConfig.ContextHandoff bool` (`context_handoff`, default false).

**Логика:** перед авторестартом по контексту сгенерировать хендофф (что сделано /
что осталось / принятые решения / изменённые файлы) — вход: последние N шагов
транскрипта (LN-01) и текущее состояние TodoWrite; дистилляция дешёвой моделью,
схема `HandoffJSONSchema`. Затем стартовать **без** `--resume`, отправив хендофф
первым user-turn'ом.

**Инвариант:** путь crash-recovery не меняется — он по-прежнему использует
`--resume` (там нужна именно история, а не сводка). Хендофф применяется только к
рестарту по контексту.

**Готово когда:** e2e на существующем сценарии роста контекста
(`testdata/scenarios/`): второй запуск CLI не содержит `--resume`, а первый
user-turn содержит маркер хендоффа; тест что при выключенном флаге поведение
прежнее.

## LN-16: Алерты регрессии стоимости

**Depends on:** LN-12
**Files:** `internal/experience/regression.go`, `regression_test.go`,
`internal/session/manager.go`, `frontend/src/components/CostDashboard.svelte`,
`frontend/src/components/StatusBar.svelte`

Замыкает инвариант 5: если скилл протух, primer раздулся, а журнал оброс — это
должно быть видно, а не растворяться в счёте.

**Детектор:** скользящая медиана `input_tokens` и `total_cost_usd` по последним
10 прогонам той же сессии. Новый прогон дороже 2× медианы (или входной контекст
на старте вырос вдвое) → событие `experience:regression` с полями
`{project, session, run_id, factor, hint}`.

**Подсказка о причине:** посчитать размер того, что менеджер сам добавляет в
промпт — primer (LN-05), число и суммарный размер описаний скиллов (LN-10),
хвост журнала (LN-06) — и включить в `hint`, если сумма выросла с прошлого
прогона.

**UI:** баннер в `CostDashboard` + счётчик в `StatusBar`; закрывается кликом, не
персистится.

**Готово когда:** тесты детектора на ряде значений (рост, шум, мало данных);
Playwright на появление баннера по событию.

## LN-17: Бэкенд markdown-логов и массовый импорт корпуса

**Depends on:** LN-02
**Files:** `internal/experience/mdlog.go`, `mdlog_test.go`,
`internal/experience/indexer.go`, `app.go`, `ExperiencePanel.svelte`,
`testdata/logfiles/*.md`

**Приоритет: сразу после LN-02, до LN-03.** JSONL-транскрипты живут только на
машине, где шла сессия, и чистятся; автосохранённые markdown-логи копятся в
проекте и переносятся между машинами. Реальный доступный корпус — именно они:
6207 прогонов, 231 МБ (см. «Что показал корпус»). Без этой задачи всё
остальное ждёт, пока накопится своя история.

**Формат** — вывод `store.RenderExport(…, "md")`, стабильный и уже
зафиксированный в коде:

```
# Session log — <project>/<session>

_Saved <RFC3339>, <N> entries._

- `HH:MM:SS` **<level>** <message>
  - tool: `<ToolName>` <toolInput>
```

Уровни: `system`, `user`, `text`, `thinking`, `tool`, `tool_result`, `result`,
`error`. `escapeMarkdown` схлопывает переводы строк в два пробела — значит
многострочная команда приходит одной строкой, и восстановить её построчно
нельзя; для сигнатуры этого достаточно (берётся первый сегмент), но в `arg`
надо класть строку как есть и **не** пытаться её «разсхлопнуть».

**Что доступно и чего нет.** Есть: последовательность вызовов с полным
`toolInput`, размер каждого `tool_result` (по длине сообщения следующей записи
уровня `tool_result`), записи уровня `error`, имя сессии и время из имени файла,
и — главное — **один файл = один завершённый прогон**, то есть `DistinctRuns`
считается напрямую. Нет: `usage`/токенов на шаг, `tool_use_id`, привязки
`is_error` к конкретному вызову, `run_id`. Потребители обязаны это переживать
(см. LN-08: `outcomeWeight = 0.6` и флаг `Imported`).

**Привязка результата к вызову — очередь FIFO, а не «следующая запись».**
Наивное правило «`tool_result` сразу за `tool`» покрывает лишь 87.4 % вызовов
(замер по 79 729 вызовам lumen) и систематически теряет самое интересное.
Ломают его три вещи, и все три обязательны к обработке:

1. **Параллельные вызовы.** Модель выдаёт несколько `tool_use` в одном
   сообщении: подряд идут 2-11 записей `tool`, затем столько же результатов
   (3443 случая). Поэтому — очередь: каждая запись `tool` кладётся в хвост,
   каждый результат снимает голову.
2. **`tool_progress`-хартбиты.** Долгие команды порождают записи уровня
   `system` с сырым JSON `{"type":"tool_progress","tool_use_id":…}` — в lumen
   это **20 399 записей, 9.2 % всего лога** (см. примечание ниже). Границу
   вызова они не образуют: пропускать, не сбрасывая очередь.
3. **Провал команды приходит уровнем `error`, а не `tool_result`** (1592
   случая). Оба уровня закрывают вызов; `error` дополнительно отмечает его как
   неуспешный — это и есть источник B для LN-07.

Всё остальное (`text`, `user`, `result`) очередь сбрасывает.

Цена ошибки здесь не косметическая: с наивным правилом медленные команды
выпадают из выборки целиком, и профиль длительностей (LN-18) показывает
«максимум 109 с» там, где на самом деле есть вызовы по 10 минут.

> **Побочная находка — дефект вне этого блока.** `tool_progress` не разбирается
> в `internal/session/parser.go`, поэтому его сырой JSON попадает в лог как
> запись уровня `system`: 9.2 % записей и 3.6 % объёма логов lumen — мусор в UI,
> в SQLite и в автосохранённых файлах. Чинится отдельно, в парсере (по образцу
> того, как молча отбрасывается `stream_event`), и к слою опыта отношения не
> имеет — но пока не починено, ingest обязан этот шум фильтровать.

**API:** `ParseLogFile(path string) (Trajectory, error)` — тот же тип, что у
LN-01. `IngestDir(root, project string, opts ImportOpts) (ImportStats, error)`:
рекурсивный обход, поддержка обоих форматов по расширению (`.jsonl` → LN-01,
`.md` → этот бэкенд), пропуск уже импортированных файлов по
`(имя, размер, mtime)`, строки пишутся с `run_id = NULL` и
`cli_session_id = <имя файла>`. Прогресс — событие `experience:import`
(файлов обработано / всего), иначе импорт 6000 файлов выглядит как зависший UI.

**UI:** в панели «Experience» — кнопка «Импорт логов…» (`PickDirectory`),
выбор проекта назначения, прогресс, итог (`ImportStats`: файлов, прогонов,
действий, пропущено, ошибок разбора).

**Готово когда:** тесты разбора на трёх фикстурах (обрезанных до ~50 записей и
с вычищенными путями — из корпуса каждого проекта); **три отдельных теста
привязки**: параллельные вызовы (3 `tool` подряд → 3 результата в том же
порядке), хартбиты между вызовом и результатом, провал уровнем `error`; тест
повторного импорта того же каталога (нулевой прирост строк); тест что файл с
битой записью импортируется частично и считается в `ImportStats.Errors`.
Приёмка: импорт `D:\temp\project-logs-20260908` отрабатывает целиком и даёт в
топе tbank ожидаемую git-преамбулу из LN-08.

## LN-18: Профиль длительностей команд и таймауты

**Depends on:** LN-17
**Files:** `internal/experience/duration.go`, `duration_test.go`,
`internal/experience/primer.go` (секция), `internal/store/migrations.go`
(колонка `dur_sec`), `ExperiencePanel.svelte`

Модель не знает, сколько идут команды в этом проекте, и узнаёт это единственным
доступным ей способом — упираясь в таймаут. Менеджер знает: время каждого
вызова считается из меток записей (`ts` вызова → `ts` результата) в обоих
бэкендах ingest.

**Замер, обосновывающий задачу** (lumen, 79 729 вызовов с корректной привязкой
из LN-17): 3465 вызовов дольше минуты, 1806 дольше двух, 530 дольше пяти.
Медианы по сигнатурам: `bash scoped-test.sh …` 155-399 с (p90 — 602 с, то есть
упирается в потолок), `timeout … --binary …` 582 с, `git worktree add -b` 120 с
при **31 провале из 90 вызовов**, `sleep <ARG>` — 953 вызова с медианой 108 с
(суммарно около 28 часов ожидания). Для контраста tbank: всего 6 вызовов
дольше двух минут — профиль обязан быть **на проект**, а не общий.

**Что делать:**

1. Колонка `dur_sec INTEGER DEFAULT 0` в `action_signatures`, заполняется при
   ingest (0 = неизвестно; наивная привязка LN-17 дала бы систематически
   заниженные значения, см. там).
2. `DurationProfile(project string) []SignatureDuration` — медиана, p90,
   максимум, доля провалов, число вызовов; только сигнатуры с n ≥ 10.
3. Секция в тёплом бриф (LN-05), не более 5 строк, только сигнатуры с
   медианой ≥ 60 с: «в этом проекте `<команда>` идёт ~N мин — ставь таймаут с
   запасом либо разбивай». Это прямое продолжение уже существующего
   `backgroundTaskWarningPrompt`: тот запрещает фоновые задачи, но не говорит,
   сколько на самом деле ждать.
4. Вкладка «Timing» в `ExperiencePanel` — та же таблица для человека.

**Отдельный сигнал — `sleep`.** Высокая доля `sleep` с большой медианой это
антипаттерн «жду фоновую задачу», который в этой архитектуре не работает
(см. «Background-Task Warning» в `docs/design-decisions.md`). Выносить в отчёт отдельной
строкой с суммарным временем — это прямой ответ на вопрос «куда ушло время
прогона».

**Готово когда:** тест расчёта длительности на фикстуре с параллельными
вызовами и хартбитами (значения обязаны совпасть с ручной разметкой), тест
что вызов без результата даёт `dur_sec = 0` и не искажает медиану, тест
секции бриф (порог 60 с, потолок 5 строк), тест профиля на двух проектах с
разным характером (быстрый Go / медленный Rust).

---

## Граф зависимостей

```
LN-01 (чтение транскриптов)
└── LN-02 (сигнатуры + схема БД)
    ├── LN-17 (бэкенд md-логов + импорт корпуса)   ← делать сразу после LN-02
    │   └── LN-18 (профиль длительностей, таймауты) ← также нужен LN-05
    └── LN-03 (индексация + вкладка Actions)
        ├── LN-04 (allowlist разрешений)
        ├── LN-05 (тёплый бриф)
        │   └── LN-06 (журнал проекта)
        │       └── LN-15 (хендофф вместо --resume)
        ├── LN-07 (провалы → правила)
        │   └── LN-08 (кандидаты + скоринг)
        │       └── LN-09 (дистиллятор скиллов)
        │           └── LN-10 (аппрув и запись)
        │               └── LN-11 (замер эффекта, протухание)
        ├── LN-12 (атрибуция токенов)
        │   └── LN-16 (алерты регрессии)
        ├── LN-13 (роутинг по исходу)
        └── LN-14 (кэш-ориентированный порядок)
```

## Параллельные группы

После LN-03 независимы и могут идти в разных worktree:

- **Группа A:** LN-04, LN-05, LN-12, LN-13, LN-14
- **Группа B:** LN-07 → LN-08 → LN-09 → LN-10 → LN-11 (строгая цепочка скиллов)
- **Группа C:** LN-06 → LN-15 (после LN-05)

## Порядок ценности

Если делать не всё: **LN-01…LN-03** дают наблюдаемость и отвечают на вопрос,
есть ли вообще сигнал (без корпуса всё остальное — гадание). **LN-04** и
**LN-05** окупаются быстрее скиллов и дешевле в реализации. Цепочка скиллов
(LN-08…LN-11) имеет смысл только вместе с LN-11: скиллы без замера — это рост
контекста без доказанного выигрыша.

**Состояние корпуса на 2026-09-08:** локальная `~/.claude-manager/history.db`
пуста (4 прогона), но привезён внешний корпус — `D:\temp\project-logs-20260908`,
6207 автосохранённых логов, 231 МБ, три проекта. Разведка по нему проведена (см.
«Что показал корпус»), пороги и правила нормализации в LN-02/07/08/12 выведены
из замеров. Поэтому цепочка **LN-01 → LN-02 → LN-17** (импорт корпуса) — первая:
после неё LN-03 и далее работают на реальных данных, а не ждут, пока накопится
своя история.

## LN-19: Нормализация путей при импорте markdown-логов

**Depends on:** LN-17
**Files:** `internal/experience/indexer.go`, `indexer_test.go`,
`internal/experience/mdlog.go`, `app.go`

**Делать до массового импорта (LN-20), иначе БД наполнится мусорными
сигнатурами, которые придётся переиндексировать.**

`actionRows` считает сигнатуру как `Signature(step.ToolName, step.InputText,
traj.ProjectPath)`. У markdown-бэкенда (`ParseLogFile`) `Trajectory.ProjectPath`
пуст — в формате `RenderExport("md")` пути проекта просто нет. В результате путь
не релятивизуется, и правило «первые два сегмента каталога» выдаёт
`Read:D:/RustProjects/*.md`, то есть весь репозиторий схлопывается в одно ведро,
а префикс машины попадает в сигнатуру. Это ровно то, что замер по корпусу
запретил (см. «Что показал корпус», п. 6), и оно ломает две вещи сразу:
одни и те же файлы получают разные сигнатуры в live-ингесте
(`Read:crates/shell/*.rs`) и в импорте (`Read:D:/RustProjects/*.rs`), а самый
«жирный» кандидат LN-08 оказывается бессмысленным.

Замер на 2026-09-15 (временный импорт логов lumen в отдельную БД, 5790 файлов,
92 678 действий): второй кандидат по оценке — `Read:D:/RustProjects/*.md`,
834 прогона, оценка 7838.

**Что сделать.** Протащить путь проекта до `actionRows`:
`IngestDir(st, root, project, projectPath string, opts ImportOpts)` (и
`ingestOne` следом), использовать `traj.ProjectPath`, когда он есть, и
переданный `projectPath` — когда нет. Путь берётся из
`config.ProjectConfig.Path` вызывающей стороной (`app.go`), не угадывается из
`root`: каталог логов может лежать где угодно, в том числе быть привезённым с
другой машины.

**Привезённый корпус — отдельный случай.** Пути внутри чужого лога
(`D:\temp\project-logs-…`) не совпадут ни с каким локальным `projectPath`.
Требование минимальное: сигнатура не должна содержать префикс машины. Достаточно
отбрасывать корневые сегменты до первого, совпадающего с именем каталога
проекта, а при отсутствии совпадения — резать абсолютный путь до последних двух
сегментов, но **никогда** не оставлять в сигнатуре имя диска.

**Готово когда:** тест, что `.md`-лог с абсолютными windows-путями и тот же
набор вызовов из `.jsonl`-транскрипта дают одинаковые сигнатуры; тест
привезённого корпуса (путь не совпадает с `projectPath` → в сигнатуре нет
`D:/` и нет `<диск>/<temp-каталог>`); тест, что `traj.ProjectPath` по-прежнему
имеет приоритет над аргументом.

## LN-20: Кнопка импорта корпуса логов (UI к IngestDir)

**Depends on:** LN-19
**Files:** `app.go`, `internal/session/manager.go` (событие),
`frontend/src/components/ExperiencePanel.svelte`,
`frontend/src/stores/experience.ts`, `frontend/src/lib/locales/*/experience.ts`,
`app_test.go`, `frontend/tests/experience.spec.ts`

**LN-17 сдан без своей UI-части**, и это блокирует весь слой опыта целиком:
`grep -rn IngestDir` по репозиторию находит только определение и тесты — ни
метода на `App`, ни кнопки. 245 МБ готовой истории лежат в
`<project>/.claude-manager/logs/` и не видны ни одному потребителю.

Замер на 2026-09-15 по рабочей `~/.claude-manager/history.db`:

| | Lumen browser |
|---|---|
| `session_runs` | 9270 |
| `action_signatures` | 568 строк, **9 прогонов** |
| `imported_logfiles` | 0 |
| md-логов на диске | 5791 файл, 245 МБ |

Кандидатов в скиллы на этих 9 прогонах — 24 штуки с максимальной оценкой 6.9
(порог дистилляции 10.0, то есть ноль доступных). После пробного импорта тех же
логов во временную БД: 946 прогонов, **379 кандидатов, 378 из них выше порога**,
p50 = 782, максимум 8692; в топе — переоткрытый git-протокол проекта
(`worktree-pool.sh` → `merge --no-ff` → `push`). Импорт занял 40 секунд.

**Что сделать.** `App.ImportProjectLogs(project string, dir string)
(experience.ImportStats, error)`: при пустом `dir` — каталог проекта по
умолчанию (`<project>/.claude-manager/logs/`), иначе выбранный через
`PickDirectory` (привезённый корпус). Гейт — тот же
`[optimization] experience_tracking`, что у LN-03/04/12/18; нет store — ошибка,
а не тихий no-op. Прогресс — событие `experience:import`
(`{project, processed, total}`) через тот же `Emitter`, что и остальные события,
по образцу `plan:roadmap_progress`; вызов долгий, без прогресса читается как
зависший UI (это уже прописано в LN-17).

**UI:** в «Experience» — кнопка «Импорт логов» рядом с «Обновить», с выбором
«каталог проекта / выбрать каталог…», строка прогресса и итог `ImportStats`
(файлов, прогонов, действий, пропущено, ошибок). Повторный импорт того же
каталога обязан быть дешёвым no-op'ом (`imported_logfiles` уже это умеет) и
показывать «пропущено: N».

**Готово когда:** тест `ImportProjectLogs` на временном каталоге с двумя
`.md`-логами (строки появились, `ImportStats` совпадает); тест повторного
вызова (нулевой прирост строк, `Skipped` = число файлов); тест, что при
выключенном `experience_tracking` каталог вообще не читается; Playwright-тест
кнопки и рендера итога на застабленном `ImportProjectLogs`.

## LN-21: Почему live-ингест видит 0.1 % прогонов

**Depends on:** LN-20
**Files:** `internal/experience/indexer.go`, `internal/session/manager.go`,
`internal/store/store.go`, `app.go`

Разовый импорт (LN-20) закрывает историю, но не будущее: после него история
снова перестанет накапливаться. Замер на 2026-09-15: у Lumen 9270 прогонов в
`session_runs` и 9 прогонов в `action_signatures`; `ingest_state` — 23 строки
**на всю базу**, при 2735 прогонах за последние 10 дней и включённом
`experience_tracking`. То есть `IngestRun` почти всегда возвращает ошибку или
выходит рано, и никто этого не видит: вызов идёт в fire-and-forget горутине из
`finishRun`.

**Сначала измерить, потом чинить.** Гипотезы, которые надо развести, а не
угадать: (а) 82 % прогонов Lumen имеют статус `error` и умирают до события
`system/init`, значит `cliSessionID` пуст и `IngestRun` выходит по первому
`if` — это штатно, но должно быть видно в счётчике, а не молча; (б)
`FindTranscript` не находит файл, потому что CLI-транскрипты чистятся или лежат
под slug'ом worktree, а не корня проекта (в `~/.claude/projects/` видны десятки
каталогов вида `D--GoProjects-MetalSwing--claude-worktrees-…`) — полный обход
`root` это покрывает, но стоит дорого и может отваливаться по времени; (в)
ошибка `InsertActions`/`SetIngestOffset`.

**Что сделать.** Логировать исход каждого вызова `IngestRun` в `app.log`
(`experience.ingest_run` с `project`, `cli_session_id`, `rows`, `reason` для
раннего выхода) и различать «нечего индексировать» и «не смог» — сейчас оба
случая выглядят одинаково. Далее — устранить доминирующую причину по замеру.
Если это (б), падать обратно на markdown-лог того же прогона: он всегда есть,
его пишет сам менеджер (`SaveSessionLogFile`), и парсер для него уже готов
(`ParseLogFile`, LN-17) — тогда ингест перестаёт зависеть от чужого каталога и
его политики очистки.

**Готово когда:** покрытие измерено до и после (доля прогонов с хотя бы одной
строкой `action_signatures` за окно), число в отчёте задачи; тест раннего
выхода при пустом `cliSessionID`; тест фолбэка на markdown-лог, когда
транскрипт не найден; тест, что повторный вызов после фолбэка не дублирует
строки.

**Результат.** Замер до фикса на реальной БД (`~/.claude-manager/history.db`,
2026-09-15): 21 из 9933 прогонов во всём приложении имеют хотя бы одну строку
в `action_signatures` (0.21%), для Lumen за последние 10 дней — 11 из 2692
(0.41%). Проверка гипотез (а)/(б) прямо на этой БД: у Lumen **100%** прогонов
несут непустой `cli_session_id` (гипотеза (а) не объясняет провал), и для
2692 прогонов за 10 дней прямой путь `FindTranscript` (без фолбэка) находит
файл в **99.6%** случаев (2682/2692, все не пустые) — гипотеза (б) тоже не
основная причина здесь. Прямой вызов `IngestRun` с правильными
`(run_id, cli_session_id)` из БД по 10 реальным завершённым прогонам Lumen
успешно проиндексировал 9 из 10 (десятый — транскрипт физически удалён с
диска, ожидаемый случай).

Настоящая причина — гонка порядка операций в `Session.Run()` (`default:` в
цикле в session.go), не входившая в исходные гипотезы (а)/(б)/(в): при
завершении задачи `s.CLISessionID` ротировался в новый UUID **до**
`s.emit(SessionEvent{Type: EvtTaskDone, ...})`, а `SessionManager.finishRun`
читал `ms.session.CLISessionID` уже после ротации — то есть на каждый
завершённый автономный прогон `IngestRun` получал id **следующей**, ещё не
запущенной задачи, для которой транскрипта заведомо не существует. Для путей
`error`/`stopped` ротация происходит уже после соответствующего события, и
там объект `ms.session.CLISessionID` на момент `finishRun` ещё корректен —
поэтому баг проявлялся только на `completed`, то есть на подавляющем
большинстве прогонов, для которых вообще стоило бы что-то индексировать.
Живой пример этого бага виден в `app.log` этой же сессии: `session:index_run_failed`
для собственного `Developer 1` с `cli_session_id`, который на момент ошибки
ещё не был подтверждён событием `system/init`.

Исправление: `EvtTaskDone` теперь несёт `CLISessionID` завершившегося
прогона, захваченный до ротации (`SessionEvent.CLISessionID`), а ротация
`s.CLISessionID` перенесена на **после** `s.emit(...)`; `finishRun` берёт
этот id из события для `completed` и только для `error`/`stopped`
по-прежнему читает `ms.session.CLISessionID` (там это по-прежнему корректно).
Регрессионный тест `TestRun_EvtTaskDone_CarriesFinishedRunsOwnCLISessionID`
(`internal/session`) гоняет два реальных прогона через `fakeclaude` и
проверяет, что `EvtTaskDone.CLISessionID` каждого прогона совпадает с тем id,
с которым был запущен именно этот процесс (не следующий) — вручную проверено,
что при откате правки только этот тест падает.

«После» на живом трафике не измерялось (нужны часы автономных прогонов),
но правильность механизма подтверждена: (1) регрессионный тест выше падает
на старом порядке операций и проходит на новом; (2) прямой повтор `IngestRun`
по всем 10 реальным прогонам Lumen с правильными идентификаторами даёт 9/10
успешных индексаций, то есть сам путь индексации был рабочим — единственной
проблемой была невозможность до него добраться с верным `cli_session_id`.

## LN-22: Вес исхода — по шагу, а не по всему прогону

**Depends on:** LN-20
**Files:** `internal/experience/candidate.go`, `candidate_test.go`

`outcomeWeight` даёт `error → 0.0`, то есть прогон, упавший на сотом шаге, не
даёт вообще никакого вклада в оценку — хотя первые девяносто девять шагов он
выполнил успешно и наблюдение по ним достоверно. На реальных данных это не
краевой случай, а норма: у Lumen 7593 прогона из 9270 имеют статус `error`,
и в текущей выборке live-ингеста (9 прогонов) **нет ни одного `completed`** —
потолок оценки там втрое ниже возможного чисто из-за весов, и порог 10.0
недостижим принципиально.

**Что сделать.** Вес считать по самому шагу, а не по итогу прогона: шаг с
`is_error = 0` — полноценное наблюдение независимо от того, чем кончился
прогон; шаг с `is_error = 1` — нулевое. Итог прогона остаётся модификатором
(`completed` — полный вес, прерванный — сниженный), но не обнуляет всё.
Конкретную формулу вывести из замера распределения оценок до и после на
импортированном корпусе (LN-20 его уже даёт), а не назначить умозрительно.
Инвариант LN-08 «неудачный прогон не может вытянуть плохой паттерн наверх»
обязан сохраниться: n-грамма, у которой ошибочны сами шаги, по-прежнему не
набирает очков.

**Готово когда:** тест, что прогон `error` с успешными шагами даёт ненулевой,
но меньший вклад, чем такой же `completed`; тест, что n-грамма из ошибочных
шагов не набирает очков ни при каком статусе прогона; таблица «оценка до / после»
по топ-20 кандидатов импортированного корпуса в описании PR.

**Результат.** Формула стала пошаговой: `stepOccurrenceWeight(rows, status)`
проверяет `IsError` у каждой строки, которую покрывает конкретное вхождение
n-граммы, — если хоть одна ошибочна, вхождение даёт 0 независимо от статуса
прогона; иначе вес берётся из `outcomeWeight(status)`, применяемого теперь не
ко всему прогону разом, а к каждому вхождению отдельно. `outcomeWeight`
изменён: `completed` — 1.0 (без изменений), `stopped`/`rate_limited`/`error` —
0.3 (раньше `error` был 0.0), неизвестный статус (bulk-импорт) — 0.6 (без
изменений). Обоснование объединения `error` с `stopped`/`rate_limited`: с
точки зрения одного успешного шага упавший на другом шаге прогон неотличим от
прерванного — то, что прогон не дошёл до чистого завершения, не говорит
ничего о достоверности конкретного удачного шага.

Замер на реальном корпусе Lumen (5805 md-логов, `IngestDir`, 950 прогонов,
92904 строк `action_signatures`) с синтетическим распределением статусов
82% `error` / 18% `completed` (воспроизводит измеренное реальное соотношение
`session_runs` Lumen: 7593/9270 = 81.9% `error`), топ-20 кандидатов по новой
оценке:

| sig | score (до) | score (после) |
|---|---|---|
| `Bash:grep -n <ARG> <ARG>` | 2269.62 | 5929.89 |
| `Bash:git status --short` | 1919.67 | 5062.35 |
| `Bash:sed -n <ARG> <ARG>` | 1950.86 | 4951.83 |
| `Edit:.claude/worktrees/*.md` | 1712.05 | 4471.08 |
| `Read:.claude/worktrees/*.md` | 1575.95 | 3929.63 |
| `Bash:git branch --show-current` | 1396.02 | 3927.17 |
| `Bash:git pull <ARG> <ARG> <ARG>` | 1424.24 | 3908.84 |
| `Read:.claude/worktrees/*.rs` | 1305.34 | 3792.96 |
| `Read:*.md` | 1464.92 | 3668.06 |
| `Edit:.claude/worktrees/*.rs` | 1227.19 | 3520.79 |
| `Bash:bash worktree-pool.sh <ARG> <ARG> <ARG>` | 1150.55 | 3369.10 |
| `Bash:bash worktree-pool.sh <ARG> <ARG>` | 1206.19 | 3174.43 |
| `Read:...md > Edit:...md` | 1257.80 | 3161.94 |
| `Read:...rs > Edit:...rs` | 1124.85 | 2959.11 |
| `Bash:git push <ARG> <ARG> <ARG>` | 876.54 | 2858.00 |
| `Bash:ls <ARG>` | 1156.51 | 2817.89 |
| `grep > grep` | 959.09 | 2722.67 |
| `Bash:wc -l <ARG>` | 865.06 | 2681.73 |
| `grep > sed -n` | 1156.61 | 2665.49 |
| `Bash:git merge --no-ff <ARG> -m <ARG>` | 847.39 | 2632.82 |

Оценка выросла в 2.5-2.9 раза по всему топ-20 — ожидаемо: при 82% `error` по
старой формуле почти 4/5 наблюдений давали нулевой вклад, по новой они
считаются на 0.3 вместо 0.0 (это единственное отличие между старой и новой
таблицами здесь: реальные `is_error` в этом импортированном корпусе редки —
2275/92904 ≈ 2.4% строк — так что эффект маскирования ошибочных шагов на
этом топ-20 почти не виден, виден именно эффект снятия жёсткого нуля со
статуса `error`). Число кандидатов выше порога 10.0 почти не изменилось
(365→366 из 367) на этом конкретном распределении, потому что верхушка и
так была далеко от порога; выигрыш — не в пересечении порога, а в том, что
у проекта с реальной долей `error` за 80% (как у Lumen) оценка вообще
отражает объём накопленного опыта, а не обнуляется весами.

## LN-23: Калибровка порога дистилляции

**Depends on:** LN-20, LN-22
**Files:** `internal/analysis/skill.go`, `internal/experience/candidate.go`,
`app.go`, `frontend/src/components/SkillReview.svelte`,
`frontend/src/stores/experience.ts`

`DefaultSkillMinScore = 10.0` — единственная некалиброванная константа блока,
и она бесполезна в обе стороны сразу. На текущей БД она режет **всё** (максимум
6.9 из 24 кандидатов), на импортированном корпусе пропускает **всё** (378 из 379,
p50 = 782, максимум 8692). Абсолютный порог над величиной, которая
масштабируется как `weightSum * log(1+rediscoveryChars)`, не может работать: она
растёт с размером корпуса.

**Что сделать.** Заменить абсолютный порог на относительный — перцентиль
собственного распределения проекта или просто верхние N кандидатов (по образцу
`DefaultMinRunShare`, который ровно по этой причине сделан долей, а не
числом). Порог остаётся защитой от «дистилляции ради дистилляции», но
дистилляция и так запускается явным кликом, а не автоматически, поэтому
основную работу должно делать ранжирование, а не отсечка.

Вывести `minScore` в UI: параметр уже прокинут насквозь
(`experience.ts` → `app.go` → `manager.go` → `analysis.DistillSkill`),
`SkillReview.svelte` шлёт жёсткий `0`. Текст ошибки «Оценка ниже порога
дистилляции» обязан называть сам порог и текущую оценку — сейчас пользователю
нечего с ним делать.

**Готово когда:** тест относительного порога на синтетическом распределении
(корпус в 10 и в 10 000 прогонов дают сопоставимую долю проходящих
кандидатов); тест, что явный `minScore` из UI перекрывает относительный; тест
сообщения об ошибке (содержит и оценку, и порог); проверка на импортированном
корпусе lumen — в шортлисте остаётся десяток кандидатов, а не 378.

## LN-24: Реальное использование скиллов и настоящая архивация

**Depends on:** LN-11
**Files:** `internal/experience/skillusage.go`, `skillfiles.go`, `skillquality.go`,
`internal/store/{store,migrations}.go`, `app.go`

Две дыры, без которых автоматика невозможна. (1) «Не используется» в LN-11
считалось по совпадению сигнатур `source_json`: навык из частой команды
(`sed -n`) совпадает всегда, загружен он или нет. Прямой сигнал — вызов `Skill`
(Claude Code) / `skill_view` (Hermes) в `action_signatures`; теперь «используется»
= загружен. (2) `ArchiveSkill` менял только статус в БД, `SKILL.md` оставался в
`.claude/skills/` и продолжал работать. Теперь папка переносится в
`.claude-manager/archived-skills/<name>-<id>/`, `RestoreSkill` возвращает её
(с ручными правками). Статусы: `draft | rejected | trial | approved | archived`;
колонки `origin`, `reason` (JSON `SkillReason`), `cost_usd`, `updated_at`.
Считаются только прогоны с проиндексированным транскриптом: непроиндексированный
не может свидетельствовать, что навык не загружали.

## LN-25: Рецензент черновиков

**Depends on:** LN-09, LN-24
**Files:** `internal/analysis/skillreview.go`, `internal/experience/skillpilot.go`

Вторая модель (по умолчанию opus, не модель-дистиллятор: свой текст она судит
снисходительно) получает черновик, кандидата и список уже имеющихся навыков
проекта и отвечает `accept` + причина. Отсекает дубли, тривиальные шаги, общие
советы и опасные шаги. Отклонённое пишется строкой `rejected` с причиной и
повторно не дистиллируется.

## LN-26: Испытание и решение

**Depends on:** LN-24, LN-25
**Files:** `internal/experience/skillpilot.go`

Принятый черновик пишется в проект со статусом `trial`. Один эксперимент за раз
(иначе до/после двух навыков не разделить). Через `SkillTrialRuns = 10`
проиндексированных прогонов: ни одной загрузки → в архив; загружался, но медиана
токенов выросла >20% или доля завершённых упала ≥0.2 → в архив; иначе → `approved`.
Принятый, но не загружавшийся `StaleRunWindow = 20` прогонов → в архив. Между
двумя автодистилляциями ≥ `AutoDistillGapRuns = 5` прогонов; дневной бюджет
`auto_skills_daily_budget_usd` (0 = без лимита). Вручную применённый навык
проекта с автопилотом тоже идёт на испытание. Уже лежащие черновики автопилот
разбирает первыми, без новой дистилляции.

## LN-27: Автопилот и вкладка «Навыки»

**Depends on:** LN-26
**Files:** `internal/session/manager.go` (`SetSkillAutopilot`, `skills:changed`),
`internal/config`, `app.go`, `frontend/src/components/SkillReview.svelte`

Флаг `auto_skills` (приватный `config.local.toml`, выкл. по умолчанию; включение
из UI включает и `experience_tracking`). Шаг автопилота запускается после
индексации каждого прогона проекта; есть кнопка «Запустить сейчас». Вкладка:
переключатель и бюджет, библиотека навыков (статус, использование: загрузок и в
скольких прогонах, эффект до→после, «почему» словами, стоимость), фильтры,
кнопки «Выключить» / «Вернуть» / «Применить всё равно»; ручная дистилляция
кандидатов сворачивается под автопилотом.

## LN-28: Хендофф по бюджету контекста посреди хода

**Depends on:** LN-15
**Files:** `internal/session/handoff_budget.go`, `handoff_budget_test.go`,
`internal/session/session.go`, `internal/config/types.go`

Исследование `docs/Science_lab` (замеры — `docs/Science_lab/MEASURE.md`): в Lumen
сессии длиннее 100 вызовов — 23 % сессий и 56 % затрат, контекст на вызов растёт
с ~59k до ~231k токенов, и каждый вызов перечитывает его целиком. LN-15 этого не
ловит: он срабатывает только на границе хода и по доле окна, а автономная задача —
обычно один ход на 50–450 вызовов.

**Флаг:** `SessionConfig.HandoffContextTokens int` (`handoff_context_tokens`,
0 = выключено, промпт байт-в-байт прежний). Только автономные Claude-прогоны.

**Логика:** вызов модели отправил ≥ N токенов контекста (usage строки `assistant`)
→ менеджер пишет в stdin записку `[session-manager <тег>]`: закончить шаг,
закоммитить WIP с запиской (сделано / дальше / как проверить), закончить ход
блоком `continue_session`. Прогон становится `slice`, следующий процесс
продолжает задачу со свежим контекстом. Тег — на процесс, о нём сказано в
системном промпте: без этого модель принимает записку, пришедшую вместе с
результатом инструмента, за инъекцию (проверено на реальном CLI). Повтор каждые
20 вызовов сверх бюджета, не больше 3 записок за прогон.

**Готово когда:** тесты — выключено = промпт и stdin без изменений; одна записка
на вызов, пересёкший бюджет; повтор и предел; интерактивный прогон не трогается.
Эффект — по `docs/Science_lab/MEASURE.md` (доля затрат в сессиях >100 вызовов,
контекст на вызов).

## LN-29: Цена перечитывания и длина сессии во вкладке «Стоимость»

**Depends on:** LN-12
**Files:** `internal/experience/attribution.go`, `attribution_lifetime_test.go`,
`internal/store/store.go` (ActionResultChars, RunLengthCosts),
`frontend/src/components/ExperiencePanel.svelte`, `stores/experience.ts`

LN-12 считает объём вывода, а платим за перечитывание: результат один раз
пишется в кэш промпта и читается заново на каждом следующем вызове сессии
(в исследовании `docs/Science_lab` — 65 % затрат). Ранний небольшой результат
длинной сессии дороже большого в её конце.

**Логика:** для каждой строки `action_signatures` — сколько шагов той же
CLI-сессии было после неё (оконная функция по `step_index`); цена =
токены × (2 + 0.1 × шагов после) в единицах некэшированного входа. Оба среза
сортируются по ней, объём остаётся рядом. Отдельно — прогоны менеджера с
известной стоимостью по полосам длины (1–20 … 251+ ходов), те же, что в
`docs/Science_lab/MEASURE.md`: видно, ушли ли затраты из длинных сессий после
LN-28.

**Готово когда:** тесты — ранний результат обгоняет поздний больший; шаги другой
CLI-сессии не считаются перечитыванием; полосы длины без прогонов без стоимости
и без числа ходов; вкладка показывает столбцы и таблицу (EXP-24a).

---

# Блок LN-30…LN-39: почему автопилот отклоняет все навыки, и память ошибок

**Замер 2026-10-23** (`history.db`, скрипт `.tmp/skill_diag.py` в главном
checkout): автопилот потратил $1.88, все 15 навыков трёх проектов — `rejected`.

1. **С 16.10 дистилляция не запускается** — 6 строк `distill_failed: fork/exec
   claude.exe: The filename or extension is too long`. Промпт идёт последним
   аргументом командной строки (лимит Windows — 32 767 символов), а
   `relateFailures` (`candidate.go`) привязывает к кандидату каждый кластер
   ошибок, случившийся *в том же прогоне*: частый `sed -n` встречается в 186 из
   218 прогонов Lumen → 61 кластер → ~115 000 символов. Та же привязка делает
   `git add`, `cat`, `sed -i` «навыками с известной ошибкой». Сбой пишется как
   отказ, и сигнатура блокируется навсегда.
2. **Кандидаты — общие приёмы чтения** (`grep -n → sed -n`, `cat`, `git status`):
   ровно то, что предписывает глобальный `CLAUDE.md`. Рецензент их справедливо
   отклоняет, а следующий вариант того же приёма (другая сигнатура) снова идёт
   на дистилляцию за $0.38.
3. **Ошибки не запоминаются.** `action_signatures` хранит только `is_error`, без
   текста; ключ кластера строится из *упавшей команды*, поэтому почти каждый
   кластер — из одного прогона. Правил из ошибок никто не делает: вкладки
   «Failures» из LN-07 нет. В транскриптах из 270 ошибок 152 начинаются строкой
   `Exit code N`, 60 — блокировкой хуком (`This session is isolated in the
   worktree …`), есть `Traceback`, `<tool_use_error>…`.

**Правила для исполнителя блока** (в дополнение к «Как сессия берёт задачу» и
«Инварианты» выше):

- Делай ровно то, что написано в «Что сделать», не расширяй объём. Если имя
  функции/строка в описании разошлись с кодом — следуй коду, а расхождение назови
  в сообщении коммита.
- Каждая новая функция — с тестом; тесты, названные в «Готово когда», обязательны.
- Никогда не перезапускай `claude-manager.exe` (см. CLAUDE.md, Known gotchas):
  проверка на живом приложении — дело пользователя, ты проверяешь тестами.
- Платные вызовы настоящего `claude` в тестах запрещены — только заглушки
  (`SkillPilotDeps`, `skillDistillFn`, helper-процесс из LN-30).

## LN-30: Промпт одноразовых вызовов `claude -p` — через stdin

**Depends on:** —
**Files:** `internal/analysis/{brief,executor,handoff,journal,preflight,skill,skillreview}.go`
и их `_test.go`, новый `internal/analysis/cliprompt.go` (+ `cliprompt_test.go`),
`cmd/fakeclaude/main.go`, `cmd/fakeclaude/main_test.go`,
`docs/design-decisions.md`

**Зачем.** Все одноразовые вызовы `claude -p` передают промпт последним
аргументом командной строки. В Windows командная строка ограничена 32 767
символами: дистиллятор навыков падает уже сейчас, а журнал/хендофф длинной
задачи упадут так же. `claude -p` без позиционного промпта читает промпт из stdin.

**Что сделать.**
1. `cliprompt.go`: `func setPromptStdin(cmd *exec.Cmd, prompt string)` —
   `cmd.Stdin = strings.NewReader(prompt)`; в doc-комментарии — причина (лимит
   32 767 символов).
2. Убрать промпт из argv в каждом построителе аргументов и выставить его через
   `setPromptStdin` в месте запуска. Места (номера строк — на 2026-10-23):
   `brief.go:68`/`:209`, `executor.go:56`/`:114`, `handoff.go:88`/`:159`,
   `journal.go:92`/`:163`, `preflight.go:100`/`:187` и `:247`/`:302`,
   `skill.go:209`/`:296`, `skillreview.go:115`/`:174`. Текст промпта
   (например, `"Distill this recurring pattern into a skill:\n\n"+task`) вынести
   в функцию рядом (`skillPrompt(task string) string` и т. п.), чтобы тесты могли
   его проверить. Если вызов передаёт `--input-format` (сессия, а не
   одноразовый вызов) — его не трогать. `internal/session/*` не трогать.
3. `cmd/fakeclaude`: в режиме `-p`/`--print` без `--input-format` прочитать stdin
   целиком; непустой (после `TrimSpace`) — это промпт для `MatchScenario`; пустой
   — прежняя логика `printModePrompt` (последний аргумент). Сессионный режим без
   изменений.
4. Тесты построителей, которые проверяли «последний аргумент = промпт», переписать:
   ни один аргумент не содержит текста задачи; функция промпта его содержит.
5. Сквозной тест `TestDistillSkill_LongPromptViaStdin`: helper-процесс через
   `TestMain` (если `os.Getenv("CM_HELPER_CLAUDE") == "1"` — читает stdin,
   проверяет маркер, печатает одну строку `{"type":"result",...}` с валидным
   `structured_output` черновика и выходит). `cfg.ClaudePath = os.Args[0]`,
   `t.Setenv("CM_HELPER_CLAUDE", "1")`, в образце команды — 100 000 символов.
   Если `TestMain` в пакете уже есть — расширить его.
6. `docs/design-decisions.md`, раздел «CLI Launch Command»: абзац «одноразовые
   вызовы получают промпт через stdin — лимит командной строки Windows».
7. Ручная проверка реального CLI (стоит центы): `echo "Ответь одним словом: ok" |
   claude -p --model haiku --output-format json` — в `result` есть ok.

**Готово когда:** `go build ./... && go vet ./... && go test ./...` зелёные;
`grep -nE 'append\(args, ".*"\+(task|sub\.Prompt)' internal/analysis/*.go`
пусто; сквозной тест проходит; ручная проверка п.7 выполнена (результат — в
сообщении коммита).

## LN-31: Сбой дистилляции не сжигает кандидата; пределы промпта дистиллятора

**Depends on:** LN-30
**Files:** `internal/experience/skillpilot.go`, `skillpilot_test.go`,
`internal/analysis/skill.go`, `skill_test.go`

**Зачем.** Сейчас любой сбой вызова (`distill_failed`, `review_failed`)
записывается строкой `rejected`, и `distillNext` больше никогда не берёт эту
сигнатуру (`known[...] = true`). В Lumen так потеряно 6 кандидатов, которых
никто не оценивал. Плюс промпт дистиллятора ничем не ограничен по размеру.

**Что сделать.**
1. `skillpilot.go`: `func decodeSkillReason(s string) SkillReason` (пустая или
   битая строка → нулевое значение) + тест.
2. Константа `MaxDistillAttempts = 3`. В цикле `distillNext` строки со статусом
   `rejected` и кодом `ReasonDistillFailed` или `ReasonReviewFailed` **не**
   кладутся в `known`; вместо этого считается число таких строк на `SigKey`.
   После цикла сигнатуры с ≥ `MaxDistillAttempts` сбоями — в `known`. В
   `lastAuto` (интервал `AutoDistillGapRuns`) и в `spentToday` сбойные строки
   по-прежнему учитываются — интервал защищает от шквала повторов.
3. `reviewDraft`: ошибка вызова рецензента больше не отклоняет оплаченный
   черновик. В `SkillReason` новое поле `Attempts int \`json:"attempts,omitempty"\``;
   при ошибке — статус остаётся `draft`, `Code = ReasonReviewFailed`,
   `Attempts++` (через `UpdateSkillStatus(id, draft, reason, now)`); при
   `Attempts >= MaxDistillAttempts` — `rejected`, как раньше.
4. Порог `ResolveSkillMinScore(candidates, 0, 0)` в `distillNext` считается
   сейчас по *всем* кандидатам, включая уже разобранные (`known`). Когда верхние
   3 % разобраны, ни один оставшийся кандидат порог не проходит и автопилот
   навсегда пишет `no_candidate`. Считать порог только по кандидатам, которые
   прошли фильтры `Kind`/`ContextLossSuspect`/`known`.
5. `analysis/skill.go`, `buildSkillTaskText` — пределы (константы рядом с
   функцией): `maxSkillSampleRunes = 600` на команду образца,
   `maxSkillFailures = 3` кластера (берутся первые — они уже отсортированы по
   `DistinctRuns`), `maxSkillFailureArgRunes = 300` на `FailedArg`/`FixedArg`,
   `maxSkillTaskRunes = 12000` на весь текст (обрезка в конце с маркером
   `… (truncated)`). Обрезка — по рунам, не по байтам (кириллица).
6. Текст ошибки в `SkillReason.Text` для `distill_failed`/`review_failed` —
   не длиннее 300 рун.

**Готово когда:** тесты: (а) после одной строки `distill_failed` следующий `Tick`
дистиллирует тот же кандидат; (б) после трёх — не берёт; (в) ошибка рецензента
оставляет черновик `draft` с `Attempts = 1`, третья — `rejected`;
(г) `buildSkillTaskText` с 60 кластерами по 5 000 символов: ≤ 12 000 рун и не
больше 3 кластеров; (д) когда лучший кандидат уже в `known`, второй всё равно
выбирается (п.4). Все прежние тесты зелёные. Шесть существующих строк
`distill_failed` становятся снова доступны без миграции — это следствие п.2,
отметить в коммите.

## LN-32: Ошибка связана с кандидатом только через его собственную команду; полные вхождения

**Depends on:** LN-31
**Files:** `internal/experience/failures.go`, `failures_test.go`, `candidate.go`,
`candidate_test.go`, `skillpilot.go` (`SkillDistillInputFromCandidate`)

**Зачем.** `relateFailures` связывает кластер с кандидатом, если они встретились в
одном прогоне. У частой команды так набираются десятки чужих ошибок. Кроме того,
`candAcc.samples` хранит только *первую* строку вхождения n-граммы: для
`grep → sed` дистиллятор видит одни `grep`. И образцы — самые старые вхождения,
а не лучшие.

**Что сделать.**
1. `FixPair` — новое поле `Sig string` (сигнатура упавшего вызова);
   `ExtractFromRun` заполняет `Sig: r.Sig`. Источник A (mixed) оставляет пустым.
2. `relateFailures(sig []string, clusters []FailureCluster)`: кластер связан, если
   у какого-либо его `Example` непустой `Sig` входит в множество шагов `sig`.
   `candAcc.runKeys` удалить, если больше нигде не используется.
3. Вхождения: в `candAcc` вместо `samples` — до `maxCandidateSamples` лучших
   вхождений `{rows []store.ActionRow (вся n-грамма, rows[i:i+n]); weight float64}`;
   «лучше» = больше `stepOccurrenceWeight`, при равенстве — новее
   (`rows[0].Timestamp`). Держать ограниченный срез, вытесняя худшее, — без
   накопления всех вхождений.
4. `SkillCandidate`: новое поле `Occurrences [][]store.ActionRow` (лучшие первыми);
   `Samples` остаётся и равен первой строке каждого вхождения, в том же порядке
   (UI и `ClassifyPermission` в `classifyCandidate` продолжают работать).
5. `SkillDistillInputFromCandidate`: один `SkillSample` на вхождение, `Command` —
   шаги построчно: для `Bash` — сам `Arg`, для остальных — `"<Tool> <Arg>"`.

**Готово когда:** тесты: кластер, чья упавшая сигнатура не входит в кандидата, не
связан даже в том же прогоне; связан, когда входит; вхождение 2-граммы содержит
обе строки; при выборе образцов вхождение из `completed`-прогона вытесняет
вхождение из `error`-прогона, а более новое — более старое при равном весе;
`SkillCandidate` с `Occurrences` переживает `json.Marshal`/`Unmarshal` (фронтенд
шлёт кандидата обратно в `App.DistillSkill`).

## LN-33: Последовательность из одного чтения — не навык; что берёт автопилот

**Depends on:** LN-32
**Files:** `internal/experience/candidate.go`, `candidate_test.go`, `skillpilot.go`,
`skillpilot_test.go`, `frontend/src/components/SkillReview.svelte`,
`frontend/src/lib/locales/{ru,en}/skillReview.ts`

**Зачем.** Все 9 отказов рецензента — последовательности из чтения
(`grep -n`, `sed -n`, `cat`, `git status`): привычки, которые уже предписаны
глобальным `CLAUDE.md`. А одиночные шаги (`reasonSingleStep`) имеют наибольший
Score и потому побеждают в `distillNext`, хотя процедуры в них нет.

**Что сделать.**
1. `const reasonReadOnlySequence = "read_only_sequence"`.
   `classifyCandidate` получает вхождения (LN-32). Новое правило — после правила
   loop и до правила «2+ шагов → skill»: `len(sig) > 1`, связанных ошибок нет, и
   каждая строка каждого вхождения проходит `ClassifyPermission(row.Tool, row.Arg)`
   → `KindNoise, reasonReadOnlySequence`. Нет вхождений → правило не срабатывает
   (отсутствие данных ничего не доказывает — как в `allSamplesReadOnly`).
   Обновить doc-комментарий правил над функцией.
2. `distillNext`: автопилот берёт только `KindSkill` с `KindReason` из
   `{reasonKnownFailure, reasonMultiStep}`; сначала `known_failure`, внутри —
   по `Score`. `single_step` остаётся видимым в ручной дистилляции во вкладке.
3. UI: `REASON_KEYS` в `SkillReview.svelte` + ключ
   `skillReview.reasonReadOnlySequence`: ru — «Только чтение — привычка, а не
   процедура», en — «Read-only sequence — a habit, not a procedure».

**Готово когда:** тесты: `grep -n → sed -n` (оба шага read-only) → `noise /
read_only_sequence`; `grep -n → Edit` → `skill / multi_step`; read-only
последовательность со связанной ошибкой (по LN-32) остаётся `skill`; автопилот
при наличии `single_step` с бо́льшим Score выбирает `multi_step`; при наличии
`known_failure` выбирает его. Фронтенд собирается (`npm run build` в `frontend`).

## LN-34: Память отказов рецензента

**Depends on:** LN-31, LN-33
**Files:** `internal/analysis/skillreview.go`, `skillreview_test.go`,
`internal/analysis/skill.go`, `skill_test.go`, `internal/experience/skillpilot.go`,
`skillpilot_test.go`, `app.go` (`DistillSkill`)

**Зачем.** Отказ запоминается только по точной сигнатуре, поэтому варианты одного
приёма (`grep-locate-then-read-range`, `read-file-slices-with-sed`,
`peek-wide-lines…`) дистиллируются заново за $0.38 каждый. Причина отказа нигде
не используется.

**Что сделать.**
1. `SkillReviewJSONSchema`: свойство `"generic": {"type": "boolean"}` (не
   обязательное). `SkillReview.Generic bool \`json:"generic,omitempty"\``.
   В `SkillReviewSystemPrompt` строка: «Set generic=true when you reject because
   the draft is generic advice, a trivial command, restates how a common tool
   works, or repeats rules already in CLAUDE.md (global or project)».
2. `SkillReason.Generic bool \`json:"generic,omitempty"\``; оба места отказа
   рецензента (`distillNext`, `reviewDraft`) его заполняют.
3. `distillNext`: `genericSteps` — объединение шагов сигнатур строк с
   `review_rejected` и `Generic`. Кандидат, у которого **каждый** шаг входит в
   `genericSteps`, пропускается.
4. `experience.RejectedBefore(st *store.Store, project string, n int) []string` —
   до `n` последних строк `review_rejected` проекта в виде
   `"<name>: <reason, ≤200 рун>"`. `analysis.SkillDistillInput.RejectedBefore
   []string`; `buildSkillTaskText` выводит раздел «Drafts already rejected in this
   project (do not produce the same kind of skill):» (входит в общий предел
   LN-31). Заполняют и автопилот (`n = 5`), и `App.DistillSkill` (ручная
   дистилляция; у `App` есть `a.store`).

**Готово когда:** тесты: разбор ответа с `generic`; после отказа с `Generic` для
`[A, B]` кандидат `[B]` и `[A, B]` пропускаются, а `[A, C]` — нет; отказ без
`Generic` ничего не блокирует, кроме своей сигнатуры; `buildSkillTaskText`
содержит раздел отказов, когда он задан, и не содержит, когда пуст.

## LN-35: Текст ошибки в `action_signatures`; ключ кластера по нему

**Depends on:** —
**Files:** `internal/store/migrations.go`, `internal/store/store.go`,
`store_test.go`, новый `internal/experience/errtext.go` (+ `errtext_test.go`),
`internal/experience/indexer.go`, `failures.go`, `failures_test.go`,
`docs/architecture.md` (схема SQLite)

**Зачем.** Индексатор сохраняет только флаг `is_error`, хотя текст ошибки у шага
есть (`Step.ResultText`, и в транскриптах, и в md-логах). Поэтому `ErrorKey`
строится из упавшей команды, и одинаковые ошибки разных команд не
группируются.

**Что сделать.**
1. Колонка `err_text TEXT` в `action_signatures`: в `CREATE TABLE` и в список
   `ALTER TABLE … ADD COLUMN` с терпимостью к «duplicate column» (образец —
   `dur_sec` в `migrations.go`).
2. `store.ActionRow.ErrText string`; запись в `InsertActions`; чтение во всех
   `SELECT`, которые сканируют полную строку (`ActionsForRun`,
   `ActionRowsForCandidates`, найди остальные по `dur_sec, ts`). NULL → `""`.
3. `errtext.go`: `func ErrorLine(text string) string` — одна осмысленная строка
   ошибки, ≤ 200 рун. Правила (из замера по 270 ошибкам транскриптов):
   - снять теги `<tool_use_error>` / `</tool_use_error>`;
   - пропустить начальную строку `Exit code N` / `Error: Exit code N`;
   - если остаток начинается с `Traceback (most recent call last):` — взять
     последнюю непустую строку (само исключение);
   - иначе среди первых 20 непустых строк — первая, содержащая (без учёта
     регистра) `error`, `fatal`, `failed`, `panic`, `not found`, `denied`,
     `cannot`, `isolated`; если такой нет — первая непустая;
   - если ничего не осталось — исходная строка `Exit code N`.
   В doc-комментарии — замер (152 из 270 начинаются с `Exit code N`).
4. `indexer.go`, `actionRows`: `ErrText: ErrorLine(step.ResultText)` только когда
   `step.ResultIsError`. Это покрывает и транскрипты, и md-логи.
5. `failures.go`, `ExtractFromRun`: `FixPair.Output = r.ErrText`. `errorKeySource`
   уже предпочитает `Output`; при пустом остаётся старый ключ по команде.
6. В тело строки ничего, кроме этой одной строки, не сохранять (приватность:
   вывод инструментов в SQLite не дублируется — см. комментарий к `SkillSample`).

**Готово когда:** табличный тест `ErrorLine` на реальных формах: `Exit code 1` +
`/usr/bin/bash: -c: line 3: unexpected EOF…`; `Traceback … ValueError: x`;
`<tool_use_error>File has not been read yet. Read it first…</tool_use_error>`;
`This session is isolated in the worktree C:\…`; пустая строка. Тест
round-trip `err_text` через `InsertActions`/`ActionRowsForCandidates`; тест, что
две разные команды с одинаковой ошибкой дают один кластер; миграция на старой БД
без колонки проходит (образец — существующие тесты миграций в `store_test.go`).

## LN-36: Дозаполнение `err_text` для уже проиндексированной истории

**Depends on:** LN-35
**Files:** `internal/store/store.go`, `store_test.go`, новый
`internal/experience/backfill.go` (+ `backfill_test.go`),
`internal/session/manager.go`

**Зачем.** Без этого кластеры по тексту ошибки начнут копиться только с нуля, а в
истории Lumen уже 322 ошибки за 90 дней.

**Что сделать.**
1. `store`: `ErrorRowsMissingText(limit int) ([]ErrTextRef, error)` — строки
   `is_error = 1 AND err_text IS NULL` (`ID`, `Project`, `CLISessionID`,
   `StepIndex`); `SetActionErrText(id int64, text string) error`.
2. `experience.BackfillErrorText(st *store.Store, projectPaths map[string]string)
   (filled, missing int, err error)`: пачками по 500 строк, группировка по
   `CLISessionID`; `FindTranscript(TranscriptsRoot(), projectPath, cliID)` →
   `ReadFrom(path, 0)` → шаги по `Index` → `ErrorLine(step.ResultText)`.
   Транскрипт не найден или шага нет → `SetActionErrText(id, "")`, чтобы строка
   не обрабатывалась повторно. Пустой `CLISessionID` → тоже `""`. Цикл кончается,
   когда пачка пуста.
3. `manager.go`: один запуск в фоновой горутине при старте менеджера, если хотя бы
   у одного проекта включён `experience_tracking`; итог — в лог
   `experience.err_text_backfill` (`filled`, `missing`). Старт не блокировать.
   Найди, где менеджер запускает индексацию (`IngestRun`), и поставь рядом.

**Готово когда:** тест на фикстуре транскрипта с ошибочным `tool_result`
(`testdata/transcripts/`, корень через `CM_TRANSCRIPTS_DIR`): строка получает
текст; строка без транскрипта получает `""` и при втором запуске не выбирается;
второй запуск возвращает `filled = 0`. Проверку на живой БД делает пользователь
после перезапуска приложения.

## LN-37: Повторяющиеся ошибки: кластеры, запись в CLAUDE.md, замер эффекта

**Depends on:** LN-35
**Files:** `internal/store/migrations.go`, `store.go`, `store_test.go`, новый
`internal/experience/failurerules.go` (+ `failurerules_test.go`),
`docs/architecture.md` (схема)

**Зачем.** LN-07 обещал «факт в CLAUDE.md» из повторяющейся ошибки, но кластеры
сейчас используются только как приписка к кандидатам в навыки. Нужен отдельный
путь «ошибка повторяется → одна строка правила → после одобрения в `CLAUDE.md` →
замер, перестала ли повторяться». Эта задача — данные и логика без LLM и UI.

**Что сделать.**
1. Таблица `failure_rules`: `id INTEGER PRIMARY KEY, project TEXT NOT NULL,
   error_key TEXT NOT NULL, examples_json TEXT, rule_text TEXT, status TEXT NOT
   NULL` (`draft | accepted | dismissed`), `distinct_runs INTEGER, cost_usd REAL
   DEFAULT 0, created_at DATETIME NOT NULL, decided_at DATETIME`,
   `UNIQUE(project, error_key)`. Тип `store.FailureRule`; методы
   `InsertFailureRule`, `ListFailureRules(project)`,
   `UpdateFailureRule(id, status, ruleText string, decidedAt *time.Time)`,
   `GetFailureRule(id)`.
2. `experience.ErrorClusters(rows []store.ActionRow) []ErrorCluster` — по **всем**
   строкам `is_error` с непустым `ErrText` (не только по парам «упало → починили»):
   ключ `ErrorKey(ErrText)`; `Count`, `DistinctRuns` (ключ прогона —
   `actionRunKey`), `LastSeen`, до 3 примеров `{FailedArg, ErrText, FixedArg}`,
   где `FixedArg` — первая следующая безошибочная строка того же прогона в
   пределах `FailurePairWindow` с той же `Sig`, иначе с тем же `Tool`, иначе `""`.
   Сортировка по `DistinctRuns`.
   Не учить шум — пропускать ошибки, чей `ErrText` начинается с:
   `The user doesn't want to proceed`, `[Tool call interrupted`,
   `Permission to use`, `read ECONNRESET`, `Request interrupted`.
   Список — константой с комментарием «не уроки, а действия пользователя или сеть».
3. `experience.FailureRuleCandidates(st, project) ([]ErrorCluster, error)`:
   `ActionRowsForCandidates(project, candidateWindowDays)` → `ErrorClusters` →
   `DistinctRuns >= MinCandidateRuns` → без ключей, уже имеющих строку в
   `failure_rules` (любой статус).
4. `experience.AppendClaudeMdRule(projectPath, rule string) error`: в
   `<projectPath>/CLAUDE.md` раздел `## Learned from failures`; нет файла —
   создать; нет раздела — дописать в конец; строка `- <rule>` — в конец раздела
   (до следующего `## ` или конца файла); такая строка уже есть — ничего не
   делать. Сохранять переводы строк файла (CRLF остаётся CRLF).
5. `experience.FailureRuleEffect(st, project string, r store.FailureRule)
   (FailureEffect, error)`: `RunsBefore/HitsBefore` — последние 20 прогонов
   проекта до `decided_at`, `RunsAfter/HitsAfter` — все прогоны после; «hit» —
   в прогоне есть строка `is_error` с `ErrorKey(ErrText) == r.ErrorKey`. Для
   не-`accepted` — нулевой результат.

**Готово когда:** тесты: два прогона с одинаковым текстом ошибки и разными
командами → один кластер, `DistinctRuns = 2`; шумовые ошибки не попадают;
`FixedArg` берётся по `Sig`, затем по `Tool`; `AppendClaudeMdRule` — создание
файла, создание раздела, вставка перед следующим `## `, идемпотентность, CRLF;
`FailureRuleEffect` на синтетике (3 из 20 до → 0 из 5 после); CRUD и
уникальность `failure_rules`.

## LN-38: Черновик правила (LLM), привязки App и MCP-инструменты

**Depends on:** LN-30, LN-37
**Files:** новый `internal/analysis/failurerule.go` (+ `failurerule_test.go`),
`internal/session/manager.go` (если нужен вызов через менеджер — как
`DistillSkill`/`skillDistillFn`), `app.go`, новый
`internal/control/mcptools_failures.go` (образец — `mcptools_skills.go`),
`docs/architecture.md`, `docs/mcp.md` (генерируется)

**Что сделать.**
1. `analysis.DraftFailureRule(ctx, projectPath string, in FailureRuleInput, cfg
   AnalysisConfig) (*FailureRuleDraft, error)`. `FailureRuleInput{ErrorKey string;
   DistinctRuns int; Examples []FailureRuleExample{FailedArg, ErrText, FixedArg}}`
   (каждое поле ≤ 300 рун). Вызов `claude -p` по образцу `ReviewSkill`:
   `--permission-mode plan`, `--output-format json`, `--json-schema`
   `{"rule": string maxLength 300, "duplicate": boolean}`, модель по умолчанию
   `sonnet`, effort `low`, **промпт через stdin** (`setPromptStdin`, LN-30).
   Системный промпт: написать ОДНУ строку-правило для `CLAUDE.md` в
   повелительном наклонении — факт о проекте или порядок действий, — которая
   предотвращает эту ошибку; на языке `CLAUDE.md` проекта; `duplicate = true`,
   если `CLAUDE.md` (глобальный или проекта) уже это говорит. `FailureRuleDraft{Rule
   string; Duplicate bool; CostUSD float64}`.
2. `App` (`app.go`), с той же проверкой `experience_tracking`, что у
   `GetSkillCandidates`:
   - `GetFailureRules(project string) ([]FailureRuleView, error)` — строки
     `failure_rules` + `Effect` (LN-37) + число ещё не разобранных кластеров
     (`FailureRuleCandidates`);
   - `DraftFailureRules(project string, limit int) ([]store.FailureRule, error)` —
     для первых `limit` (≤ 0 → 3) кандидатов вызвать `DraftFailureRule` и вставить
     строку: `draft`, а при `Duplicate` — сразу `dismissed` (чтобы не
     дистиллировать повторно); `cost_usd` записать;
   - `AcceptFailureRule(id int64, ruleText string) error` —
     `AppendClaudeMdRule(projectPath, ruleText)`, затем `accepted`, `decided_at =
     now`, `rule_text = ruleText` (пользователь мог поправить текст);
   - `DismissFailureRule(id int64) error`.
   Запись в `CLAUDE.md` — только из `AcceptFailureRule` (инвариант 4: явное
   «Принять»).
3. MCP: `get_failure_rules` (read), `draft_failure_rules` (write, платный — так и
   сказать в описании), `accept_failure_rule` (write), `dismiss_failure_rule`
   (write) — по образцу `mcptools_skills.go`; затем `go run ./cmd/cm-mcp
   -gen-docs`. Если parity-тест (`app_parity_test.go`) требует — добавить.
4. `docs/architecture.md`: строки четырёх методов в таблице «Wails Bindings
   (app.go)».

**Готово когда:** тесты: аргументы `DraftFailureRule` без промпта в argv, промпт
содержит ключ ошибки и примеры; разбор ответа (голый объект и обёртка `result`);
`DraftFailureRules` с заглушкой: `Duplicate` → `dismissed`, иначе `draft`;
`AcceptFailureRule` пишет строку в `CLAUDE.md` временного проекта и ставит
`accepted`; `TestDocsInSync` и parity-тест зелёные.

## LN-39: Вкладка «Ошибки» во `ExperiencePanel`

**Depends on:** LN-38
**Files:** `frontend/src/components/ExperiencePanel.svelte`,
`frontend/src/stores/experience.ts`,
`frontend/src/lib/locales/{ru,en}/experiencePanel.ts`,
`frontend/tests/experience.spec.ts`, `GUI-TESTS.md`

**Что сделать.**
1. Новая вкладка `'failures'` в `type Tab` и в ряд кнопок вкладок (образец —
   вкладка `skills`); ключ `experiencePanel.tabs.failures`: ru «Ошибки», en
   «Failures».
2. Содержимое (данные — `GetFailureRules`):
   - строка «Повторяющихся ошибок без правила: N» и кнопка «Предложить правила»
     → `DraftFailureRules(project, 3)`, на время вызова — индикатор и
     заблокированная кнопка; после — стоимость вызова и обновление списка;
   - **Черновики**: ключ ошибки, «в N прогонах», примеры «упало → сработало»
     (моноширинно, обрезка длинных строк), `textarea` с текстом правила, кнопки
     «Принять в CLAUDE.md» и «Отклонить». Без `window.confirm()` (CLAUDE.md,
     Conventions);
   - **Принятые**: правило, дата, эффект «до: X из N прогонов → после: Y из M»;
   - **Отклонённые** — свёрнутым списком.
3. Ошибку вызова показывать строкой в самой вкладке, а не модальным окном.
4. Playwright-тест в `frontend/tests/experience.spec.ts` с подменёнными
   привязками (образец — существующие тесты вкладок там же): список рендерится;
   «Принять» вызывает `AcceptFailureRule` с отредактированным текстом;
   «Предложить правила» блокирует кнопку на время вызова. Строка в
   `GUI-TESTS.md`.

**Готово когда:** `npm run build` и Playwright-тест вкладки зелёные, `go test
./...` зелёный; вкладка видна при включённом `experience_tracking`.

## LN-40: Автопилот сам применяет правила из ошибок (через системный промпт)

**Depends on:** LN-37, LN-38
**Files:** `internal/store/migrations.go`, `store.go`, `store_test.go`,
`internal/experience/skillpilot.go`, новый `internal/experience/rulepilot.go`
(+ `rulepilot_test.go`), `failurerules.go`, `internal/session/session.go`,
`internal/session/manager.go`, `session_wire_test.go` (или рядом),
`app.go` (там, где собираются `SkillPilotDeps`), `config.example.toml`
(комментарий к `auto_skills`), `docs/design-decisions.md`

**Зачем.** Пользователь хочет, чтобы правило из повторяющейся ошибки начинало
действовать без клика — как навыки при `auto_skills`. Писать в `CLAUDE.md`
автоматически нельзя: это файл репозитория, его правят developer-сессии при
doc-sync, и грязный `CLAUDE.md` в главном checkout ломает их `merge`. Поэтому
автоприменённое правило живёт в БД и подставляется блоком в
`--append-system-prompt` при запуске Claude-сессий проекта. Выключить — значит
сменить статус; следующая сессия правило уже не увидит. См. инвариант 4.

**Статусы `failure_rules`** (к `draft | accepted | dismissed` из LN-37):
`trial` — применено автопилотом, идёт замер; `kept` — прошло замер, остаётся в
промпте; `reverted` — снято (замер не прошёл или клик). В промпт идут `trial` и
`kept`. `accepted` = лежит в `CLAUDE.md`, в промпт не идёт (без дубля).

**Что сделать.**
1. `failure_rules`: колонки `origin TEXT NOT NULL DEFAULT 'manual'`
   (`manual | auto`) и `reason TEXT` (JSON `{code, text, hits_before,
   runs_before, hits_after, runs_after}`) — через `ALTER … ADD COLUMN` с
   терпимостью к «duplicate column» и в `CREATE TABLE`. `store.FailureRule`
   получает `Origin`, `Reason`; метод
   `InjectedFailureRules(project) ([]FailureRule, error)` — `status IN
   ('trial','kept')`, по `decided_at`.
2. `FailureEffect` (LN-37) получает `CompletedBefore`, `CompletedAfter` —
   число прогонов со статусом `completed` в каждом окне (статус уже есть в
   `CandidateActionRow.RunStatus`).
3. `rulepilot.go`: `func (p *SkillPilot) rulesStep(ctx, params, rep) error`.
   Константы: `FailureRuleTrialRuns = 10`, `MaxInjectedRules = 10`,
   `MinRecentHits = 2`. Порядок:
   - **Решение по испытанию.** Для строки `trial`: `FailureRuleEffect`; пока
     `RunsAfter < FailureRuleTrialRuns` — ждать. Затем `kept`, если доля
     прогонов с ошибкой после ≤ половины доли до **и** доля `completed` не упала
     на ≥ 0.2; иначе `reverted` с `reason.code = "no_effect"` или `"worse"`.
     Числа — в `reason`.
   - **Одно испытание за раз.** Есть строка `trial` → дальше не идти.
   - **Пределы.** `InjectedFailureRules` ≥ `MaxInjectedRules` → стоп. С
     `created_at` последнего `origin = auto` прошло < `AutoDistillGapRuns`
     проиндексированных прогонов → стоп. Дневной бюджет
     `params.DailyBudgetUSD` — **общий с навыками**: вынести подсчёт
     «потрачено сегодня автопилотом» в одну функцию, которая суммирует
     `skills.cost_usd` и `failure_rules.cost_usd` строк `origin = auto` за
     сегодня, и использовать её и в `distillNext`, и здесь.
   - **Готовый черновик первым.** Есть строка `draft` (например, после кнопки
     «Предложить правила») → применить её без нового вызова LLM.
   - **Иначе новый.** Первый кандидат `FailureRuleCandidates`, у которого
     `FailureRuleEffect` с `DecidedAt = now` даёт `HitsBefore ≥ MinRecentHits`
     (ошибка повторяется *сейчас*, а не только когда-то за 90 дней — иначе
     замер нечем подтвердить) → `deps.DraftRule` → `Duplicate` → строка
     `dismissed`; иначе строка `origin = auto`.
   - **Применить** = статус `trial`, `decided_at = now`. В `CLAUDE.md` не писать.
   Отчёт: в `SkillPilotReport` поля `RulesApplied`, `RulesKept`,
   `RulesReverted []string` (текст правила, ≤ 80 рун); `Changed()` их учитывает.
4. `SkillPilotDeps.DraftRule func(ctx, projectPath string, in
   analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error)`; в `app.go`
   подключить `analysis.DraftFailureRule` (LN-38) там же, где `Distill`/`Review`.
5. `Tick`: после `evaluate` вызвать `rulesStep`, затем `distillNext`. Ошибка
   `rulesStep` не мешает навыкам: запомнить, выполнить `distillNext`, вернуть
   первую ошибку. Ранние `return` в `distillNext` (`trial_in_progress` навыка) на
   правила не влияют — они выполнены раньше.
6. Подстановка в промпт. `session.Params.LearnedRules string` и поле сессии
   (как `ManagerCard`). В `session.go` (сборка `--append-system-prompt`, рядом с
   `card`) — если строка не пустая, дописать её отдельным абзацем; условие
   `if s.Config.SystemPromptAppend != "" || … || card != ""` дополнить
   `|| s.learnedRules != ""`. Только Claude-runtime: Hermes не трогать.
   `manager.go`: `func (m *SessionManager) learnedRulesFor(proj
   *config.ProjectConfig) string` — `""`, если `!proj.AutoSkills`, нет store или
   нет правил; иначе
   ```
   ## Learned from failures (Claude Manager)
   Errors that kept recurring in this project's past runs, with the rule that avoids each:
   - <rule_text>
   ```
   Передать во всех трёх местах, где сейчас передаётся `ManagerCard:
   m.managerCardFor(proj, name)`. Правила читаются при запуске процесса CLI, не
   посреди сессии (кэш промпта не ломается).
7. `config.example.toml`: в комментарии к `auto_skills` — «включает и
   автоприменение правил из ошибок (LN-40), они идут в системный промпт, не в
   CLAUDE.md». `docs/design-decisions.md`: абзац в разделе про автопилот навыков —
   почему системный промпт, а не `CLAUDE.md` (merge developer-сессий, worktree).

**Готово когда:** тесты: (а) **инвариант 6** — при `LearnedRules == ""` argv сессии
байт-в-байт прежний (сравнить с выводом до изменения); при непустом — блок есть в
`--append-system-prompt`; (б) `learnedRulesFor` пуст при `auto_skills = false` и
при отсутствии `trial`/`kept`, `accepted`/`reverted` в блок не попадают;
(в) `rulesStep`: черновик применяется раньше нового вызова LLM; при `trial` новых
нет; кандидат без свежих ошибок (`HitsBefore < 2`) пропускается; `Duplicate` →
`dismissed`; после 10 прогонов без ошибки → `kept`, с прежней частотой →
`reverted/no_effect`, при падении доли `completed` на 0.2 → `reverted/worse`;
(г) общий дневной бюджет: траты на правила останавливают и дистилляцию навыков;
(д) ошибка `DraftRule` не мешает `distillNext`. `go build/vet/test` зелёные.

## LN-41: Автоправила в UI и MCP: видно, выключается, возвращается

**Depends on:** LN-39, LN-40
**Files:** `app.go`, `app_test.go` (или где тесты App для навыков),
`internal/control/mcptools_failures.go`, `docs/architecture.md`, `docs/mcp.md`
(генерируется), `frontend/src/components/ExperiencePanel.svelte`,
`frontend/src/components/SkillReview.svelte`, `frontend/src/stores/experience.ts`,
`frontend/src/lib/locales/{ru,en}/{experiencePanel,skillReview}.ts`,
`frontend/tests/experience.spec.ts`, `GUI-TESTS.md`

**Что сделать.**
1. `App`: `RevertFailureRule(id int64) error` (`trial`/`kept` → `reverted`,
   `reason.code = "reverted_manual"`) и `RestoreFailureRule(id int64) error`
   (`reverted` → `kept`, `reason.code = "restored_manual"`: явный выбор
   пользователя, без нового испытания). `AcceptFailureRule` (LN-38) для строки
   `trial`/`kept` пишет её в `CLAUDE.md` и ставит `accepted` — после этого она
   уходит из системного промпта, дубля нет; проверить тестом.
   `GetFailureRules` отдаёт `origin` и разобранный `reason`.
2. MCP: `revert_failure_rule`, `restore_failure_rule` (write) — по образцу
   `mcptools_failures.go`; `go run ./cmd/cm-mcp -gen-docs`; строки в таблице
   «Wails Bindings» `docs/architecture.md`.
3. Вкладка «Ошибки» (LN-39): раздел **«Применено автопилотом»** — правило,
   статус («испытание: N из 10 прогонов» / «оставлено» / «снято»), «почему»
   словами из `reason` (до → после), кнопки «Выключить» (`trial`/`kept`),
   «Вернуть» (`reverted`), «Перенести в CLAUDE.md» (`AcceptFailureRule` с текущим
   текстом). Без `window.confirm()`.
4. `SkillReview.svelte`: подпись у переключателя автопилота — что он теперь
   применяет и правила из ошибок (через системный промпт сессий проекта); ru/en.
5. Подписка на `skills:changed` уже обновляет вкладку навыков — обновлять по ней
   и вкладку «Ошибки» (автопилот шлёт это событие, когда `Changed()`).

**Готово когда:** Go-тесты `Revert`/`Restore`/перенос в `CLAUDE.md` (строка
пропадает из `InjectedFailureRules`); `TestDocsInSync` и parity-тест зелёные;
Playwright: раздел автоправил рендерится, «Выключить» вызывает
`RevertFailureRule`, «Вернуть» — `RestoreFailureRule`; `npm run build` зелёный.
