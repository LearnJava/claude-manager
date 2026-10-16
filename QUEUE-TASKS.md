# QUEUE-TASKS — очередь сессии видна целиком и сразу

Панель «Очередь» (`TaskPanel` → `QueuePanel`, мини-бар в сайдбаре, доска)
строится из `GetSessionRoadmap` → `analysis.ReadRoadmap`
(`internal/analysis/roadmapview.go`) → `buildQueue` (`frontend/src/lib/queue.ts`).

## Почему (2026-10-15, Developer 1)

В `STATUS-P1.md` поставили WT-02 и WT-03 перед MCP-01..17, а панель
показывала «17 в очереди» и первой MCP-01. Причин две.

1. **Файл задач без таблицы статусов выпадает из очереди без следа.**
   `parseRoadmap` строит узлы только из строк markdown-таблиц. `remapPointers`
   умеет перевести указатель на заголовок `## LN-02: …` в строку таблицы-индекса,
   но если таблицы нет (а в `WT-TASKS.md` её не было), у файла `Total == 0`, и
   `ReadRoadmap` молча пропускает его (`continue // a source file…`). Карточка
   менеджера (`internal/session/managercard.go`) учит Chat писать задачи как
   `## <ID>: <title>` без таблицы — значит, так выпадет любой новый блок.
   Временно обойдено: в конец `WT-TASKS.md` дописана таблица `## Status`.
2. **Панель не видит правки файла очереди.** `syncQueue`
   (`frontend/src/stores/queues.ts`) перечитывает очередь только когда меняется
   `task_source_description` или `tasks_done`. Дописали указатели в
   `STATUS-P1.md` или поправили файл задач — панель покажет это лишь после
   следующей задачи или переключения сессии.

## Инварианты блока

- Существующие форматы не ломаются: сгенерированный `ROADMAP.md`, курируемый
  (lumen-browser, колонка `status`), баг-трекер `BUGS.md`, разбивки с
  таблицей-индексом (`LEARN-TASKS.md`, `MCP-TASKS.md`). Все тесты
  `internal/analysis/roadmapview_test.go` остаются зелёными без правок ожиданий.
- Если у файла есть таблица и строка для задачи — статус по-прежнему из неё.
  Заголовки — только запасной источник.
- Никаких новых зависимостей (в т.ч. `fsnotify`): опрос `mtime` достаточно.

## Как сессия берёт задачу

Та же схема, что у `MCP-TASKS.md`: очередь — голые указатели
`QUEUE-TASKS.md:NN` в [STATUS-P1.md](./STATUS-P1.md), статусы — таблица в конце
файла, протокол — `/cm-task-start` и `/cm-task-finish` по
[docs/git-workflow.md](./docs/git-workflow.md). Гейт:
`go build ./... && go vet ./... && go test ./...`, для QV-02 ещё
`npm --prefix frontend test`. Новые задачи дописывать в конец файла, чтобы не
сдвигать указатели.

---

## QV-01: задачи из заголовков `## <ID>: <title>`, когда в таблице их нет

**Depends on:** —

**Files:** `internal/analysis/roadmapview.go`,
`internal/analysis/roadmapview_test.go`, `docs/design-decisions.md`
(раздел про чтение роадмапа / очереди), `internal/session/managercard.go`
(только если текст карточки надо уточнить — см. шаг 5).

**What to do:**

1. В `parseRoadmap` (или сразу после него в `ReadRoadmap`) после
   `remapPointers` найти указатели из `open`, которые не попали ни в одну
   строку таблицы. Для каждого такого указателя прочитать строку файла; если
   это заголовок `^#{2,4}\s+<ID>:\s*<title>` (ID — тот же токен, что ловит
   `pointerIDRe`), создать узел `RoadmapNodeTask`: `ID` = ID, `Name` = title
   (без markdown-обёрток вроде обратных кавычек не трогать — как в таблицах),
   `Line` = строка заголовка, `InQueue = true`, `Current = Line == firstOpen`,
   `Status` = `active` для текущего, иначе `pending`, `HasDetail = true`.
2. Ещё и заголовки того же вида **без** указателя в файле, где нет таблицы
   задач вовсе: узлы со статусом `done` (модель указателей: задача сделана,
   когда её указатель ушёл из очереди) — так WT-01 после мерджа видна как
   готовая. В файле, где таблица есть, заголовки без указателя не добавлять —
   их статус знает таблица.
3. Порядок узлов — по номеру строки, вперемешку с узлами из таблицы не
   смешивать: узлы из заголовков идут после табличных, в порядке строк.
   `Done`/`Total` считать как для остальных узлов; `StatusModel` для файла
   только из заголовков — `pointer`-модель (та же константа, что у
   сгенерированного роадмапа).
4. Текст задачи по клику: `GetRoadmapRowDetail(project, file, line)` →
   `ReadRoadmapRowDetail`. Для строки-заголовка вернуть тело секции — от
   заголовка до следующего заголовка того же или более высокого уровня (или
   `---`), ограничив размер так же, как для `note`.
5. Проверить текст карточки менеджера: если после правки таблица `## Status`
   больше не нужна для видимости — ничего не менять; если какой-то формат
   по-прежнему выпадает — дописать в карточку одну фразу о нём.
6. Тесты в `roadmapview_test.go`:
   - файл только с заголовками `## AA-01:`, `## AA-02:`, `## AA-03:`, очередь
     `F.md:<AA-02>`, `F.md:<AA-03>` → 3 узла, AA-01 `done`, AA-02 `current`
     и `active`, AA-03 в очереди `pending`;
   - очередь в два файла: один с таблицей-индексом (как `MCP-TASKS.md`),
     другой только с заголовками (как старый `WT-TASKS.md`) → оба файла в
     `mergeViews`, первым текущим — указатель из первой строки очереди;
   - таблица есть, но строки для заголовка, на который указывает указатель,
     в ней нет → задача всё равно в очереди;
   - `ReadRoadmapRowDetail` на строке заголовка возвращает тело секции без
     следующей задачи.
7. `docs/design-decisions.md`: в разделе о роадмапе/очереди (рядом с
   описанием `remapPointers`) — абзац «задачи из заголовков».

**Done when:** гейт зелёный; новые тесты проходят, старые — без правок
ожиданий; на этом репозитории `ReadRoadmap(".", "STATUS-P1.md")` показывает
задачи WT и QV даже если удалить из `WT-TASKS.md` таблицу `## Status`
(проверить временно, не коммитить удаление).

---

## QV-02: панель перечитывает очередь, когда файлы на диске меняются

**Depends on:** QV-01

**Files:** `internal/session/manager.go` (или новый
`internal/session/queuewatch.go` + `queuewatch_test.go`),
`frontend/src/stores/sessions.ts`, `frontend/src/stores/queues.ts`,
`docs/design-decisions.md` (раздел «Stream-JSON / session events» — новое
событие), `docs/architecture.md` (дерево, если появился файл).

**What to do:**

1. В `SessionManager` — фоновый цикл (горутина, старт в конструкторе или в
   `SetContext`, остановка в `StopAll`/`Shutdown` через `context`/канал),
   раз в 2 с для каждой сессии с непустым `TaskSource`: `os.Stat` файла
   очереди и всех файлов, на которые в нём есть указатели (разбор указателей
   — переиспользовать регулярку из `session.go` `taskPointerPattern`),
   сравнить набор `(путь, mtime, size)` с прошлым. Изменился — эмитить
   `session:queue_changed` с `{id}` через `m.emitter`. Первый проход только
   запоминает состояние, не эмитит.
2. Не читать содержимое task-файлов в цикле — только `Stat` (+ чтение самого
   файла очереди, он маленький). Ошибки `Stat` (файл удалён) — это тоже
   изменение, без лога на каждом тике.
3. Фронтенд: в `sessions.ts` подписка `EventsOn('session:queue_changed', …)`
   вызывает `loadQueue(session)` из `queues.ts` (нужен объект с
   `id/project/name` — взять из стора сессий). Дребезг 300 мс на сессию, чтобы
   серия сохранений не давала серию запросов.
4. Тест Go: временная папка, сессия с `TaskSource`, фейковый `Emitter`;
   дописать строку в файл очереди → событие ровно одно; правка файла задач,
   на который есть указатель → событие; без изменений → ни одного. Тикер
   инжектировать (поле с интервалом), чтобы тест шёл миллисекунды.
5. Тест фронта (vitest, рядом с существующими для `queues.ts`): событие
   `session:queue_changed` вызывает `GetSessionRoadmap` повторно.
6. Документация: строка про `session:queue_changed` там, где описаны события
   `session:*`.

**Done when:** гейт зелёный (Go + `npm --prefix frontend test`); в собранном
приложении дописанный в `STATUS-P1.md` указатель появляется в панели
«Очередь» за ~2 с без переключения сессий и без завершения задачи.

---

## QV-03: остановка при пустой очереди включена у всех по умолчанию

**Зависит от:** —
**Files:** `internal/config/types.go`, `internal/config/config.go`
(загрузка и слияние слоёв конфига), `internal/session/session.go`
(`Autonomous`, проверка в `Run` ~1073, ~1534),
`internal/session/hermes_runtime.go:134`, `app.go` (`upsertP1Session`),
`frontend/src/components/Settings.svelte`,
`frontend/src/lib/sessionFeatures.ts`, локали
`frontend/src/lib/locales/{ru,en}/{settings,sessionFeatures,featureHelp}.ts`,
`config.example.toml`, `PLAN.md` (§ про `stop_when_no_tasks`, строки
~221 и ~355), `docs/design-decisions.md` (~123, ~454, ~469), тесты в
`internal/config/` и `internal/session/`.

**Проблема (2026-10-16, ARB).** У ARB → Developer 1 включён
`auto_restart`, а `stop_when_no_tasks = false`. Программист закрыл последнюю
задачу и удалил её указатель, `STATUS-P1.md` опустел, но менеджер всё равно
запустил новую сессию с промптом «работай по очереди». Она читает пустую
очередь, заканчивается, и автоперезапуск повторяет это по кругу: токены
тратятся без работы. Остановка при пустой очереди нужна практически
всегда, но сейчас её надо включать руками для каждой сессии. Значение по
умолчанию поменять недостаточно: окно настроек пишет
`stop_when_no_tasks = false` в конфиг каждой сессии явно, и у
существующих сессий ничего не изменится.

**Что сделать.**
1. Инвертировать настройку, чтобы нулевое значение означало «останавливаться»
   (соглашение проекта: ноль = выключено). Новое поле
   `SessionConfig.RunWithoutTasks bool` с ключом `run_without_tasks`. При
   `true` сессия крутит запуски при пустой очереди, как раньше при
   `stop_when_no_tasks = false`. Оставить его для очередей, формат которых
   `hasTasks` не распознаёт.
2. Проверка пустой очереди в `Run` (`session.go` ~1073): срабатывает при
   `TaskSource != "" && !RunWithoutTasks`. Поведение при срабатывании не
   меняется: обычная интерактивная сессия без автоперезапуска.
3. Автономность (`Autonomous()`, `session.go` ~1534,
   `hermes_runtime.go:134`) больше не зависит от этой настройки, только от
   `AutoRestart`.
4. Миграция старого ключа. `StopWhenNoTasks` остаётся в структуре только для
   чтения старых конфигов, с `toml:"stop_when_no_tasks,omitempty"`. После
   загрузки каждого слоя (`config.toml`, `config.local.toml`, глобальный):
   - `stop_when_no_tasks = true` и `auto_restart = false` →
     `AutoRestart = true`, потому что раньше эта пара делала сессию
     автономной;
   - `stop_when_no_tasks = false` **не** переносится в `run_without_tasks`:
     старое `false` записывал Settings, а не пользователь, и цель задачи —
     включить остановку у всех;
   - после миграции `StopWhenNoTasks = false`, чтобы при следующем
     сохранении ключ исчез из файла.
5. UI. В `Settings.svelte` вместо галочки «Останавливаться при отсутствии
   задач» сделать галочку «Крутить запуски при пустой очереди», выключенную
   по умолчанию, с подсказкой из `featureHelp`. Обновить `sessionFeatures.ts`
   и локали ru/en (`featureHelp.stopWhenNoTasks.*` переименовать в
   `runWithoutTasks.*` и переписать: по умолчанию сессия останавливается).
   Из `upsertP1Session` (`app.go` ~555, ~572) убрать `StopWhenNoTasks`.
6. Документация: `config.example.toml` (`run_without_tasks = false` с
   комментарием вместо `stop_when_no_tasks`), `PLAN.md`,
   `docs/design-decisions.md`.

**Готово когда:**
- Тест сессии на fakeclaude: `task_source` с пустым файлом,
  `auto_restart = true`, без других ключей → одна интерактивная сессия,
  повторного запуска нет.
- Тест: то же с `run_without_tasks = true` → прежнее поведение (запуск с
  промптом очереди).
- Тест загрузки конфига: `stop_when_no_tasks = true, auto_restart = false`
  → `AutoRestart = true`; `stop_when_no_tasks = false` → `RunWithoutTasks
  = false`; после сохранения ключа `stop_when_no_tasks` в файле нет.
- `grep -rn StopWhenNoTasks` вне `internal/config` и тестов миграции пуст.
- Гейт зелёный, `npm --prefix frontend test` зелёный.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| QV-01 | ✓ DONE | internal/analysis/roadmapview.go, roadmapview_test.go |
| QV-02 | ✓ DONE | internal/session/queuewatch.go, frontend/src/stores/queues.ts, sessions.ts |
| QV-03 | ✓ DONE | internal/config/, internal/session/session.go, Settings.svelte |
