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

**Очередь: [LEARN-STATUS.md](./LEARN-STATUS.md)** — только строки-указатели
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
name = "LN"
prompt = "Ты программист 1. Читай CLAUDE.md, раздел «Roles and session start», и работай по очереди LEARN-STATUS.md."
task_source = "LEARN-STATUS.md"
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
| LN-09 | ○ TODO | internal/analysis/skill.go, schema.go, internal/store (skills) |
| LN-10 | ○ TODO | frontend SkillReview.svelte, internal/experience/skillfiles.go, app.go |
| LN-11 | ○ TODO | internal/experience/skillquality.go |
| LN-12 | ○ TODO | internal/experience/attribution.go |
| LN-13 | ○ TODO | internal/optimization/routing.go, outcomes.go |
| LN-14 | ○ TODO | internal/optimization/cache.go, internal/experience/affinity.go |
| LN-15 | ○ TODO | internal/experience/handoff.go, internal/optimization/context.go |
| LN-16 | ○ TODO | internal/experience/regression.go, CostDashboard.svelte |
| LN-17 | ✓ DONE (2026-09-08) | internal/experience/mdlog.go, indexer.go (IngestDir), testdata/logfiles/ |
| LN-18 | ✓ DONE (2026-09-09) | internal/experience/duration.go, primer.go, ExperiencePanel.svelte |

> LN-17 и LN-18 дописаны после разведки корпуса. LN-17 по приоритету идёт
> **третьим**, сразу за LN-02 (без него нечего анализировать); LN-18 — после
> LN-05, т.к. пишет в тёплый бриф. Порядок берётся из `LEARN-STATUS.md`, а не
> из номеров.

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
   UI. Автоматически пишутся лишь файлы под `<project>/.claude-manager/`
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
- Автоматическую правку `CLAUDE.md` и автоприменение скиллов без человека.
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
(см. «Background-Task Warning» в `CLAUDE.md`). Выносить в отчёт отдельной
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
