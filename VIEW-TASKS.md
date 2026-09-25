# Вывод сессии в стиле Hermes — план работ (UI-01…UI-13)

Пользователю нравится, как Hermes Desktop показывает ход работы агента, и он
хочет видеть так же вывод сессии в claude-manager. Этот блок задач переносит
этот подход в `LogStream.svelte`.

## Что именно у Hermes (по записи экрана 2026-09-24)

Источник: запись экрана Hermes Desktop, 3 минуты. Лента чата читалась через
OCR кадров раз в 15 с. Картинку не описываем — перечисляем только приёмы,
которые видны в кадре:

1. **Вызовы инструментов свёрнуты в одну строку и сгруппированы.** Серия
   вызовов подряд показана одной строкой: `Выполнено python hdump.py … | cut -c
   1-280 + 3 commands`, `Ran 2 commands`, `Explored store_test.go, ran 1
   command`. Вывод команд в ленте не виден, пока строку не раскроют.
2. **Размышления свёрнуты в строку с длительностью:** `Думал 3s`,
   `Думал 5s`, `Кратко подумал`. Сам текст размышления приглушён и свёрнут.
3. **Реплики агента — обычная проза**, пропорциональный шрифт, ширина колонки
   чата. Моноширинный шрифт только внутри команд и кода.
4. **Правка файла — карточка с именем и счётчиком строк** (`store_test.go
   +25`) и встроенным диффом под ней.
5. **Итог — оформленный markdown:** заголовок `Готово…`, таблица
   «Репозиторий / Коммиты / Что изменено», раздел `Как проверено:`.
6. **Сообщение пользователя — отдельный пузырь** справа, ход агента идёт под
   ним. Лента читается как «вопрос → работа → ответ», а не как поток строк
   лога.

Что у нас сейчас (`frontend/src/components/LogStream.svelte`): каждая
`LogEntry` — отдельная строка `[время] иконка текст` моноширинным 12px.
Вызов инструмента и его результат — две несвязанные строки, размышления идут
полным текстом, реплика агента выглядит так же, как вывод `git status`.
Markdown уже рендерится (`lib/markdown.ts`), длинные записи уже сворачиваются
(`COLLAPSE_CHARS`), поиск по логу уже есть — всё это сохраняется.

## Инварианты блока

- **Классический вид остаётся.** Новый вид включается переключателем рядом с
  флажком Markdown и хранится в `localStorage`, как `stores/logView.ts:
  logMarkdown`. Выключенный переключатель даёт ровно сегодняшний
  `LogStream` — проверяется существующими spec'ами без правок.
- **Поиск (Ctrl+F) работает в обоих видах.** Группа, в которой нашлось
  совпадение, раскрывается. Счётчик `N/M` считает записи, а не группы.
- **Ключи — `seq`, не индексы**, по той же причине, что у `overrides` в
  `LogStream` (ring buffer вытесняет старые записи).
- **Ни одна запись не теряется.** Всё, что было в классическом виде, доступно
  раскрытием в новом.
- **Оба рантайма.** Claude (`internal/session/parser.go`) и Hermes
  (`internal/session/hermes_parser.go`) дают одинаковую ленту.
- **Логику группировки держать в чистом TS-модуле** (`frontend/src/lib/`),
  не в `.svelte`: её тестируют unit-spec'и Playwright по образцу
  `frontend/tests/formatters.spec.ts`.

## Как сессия берёт задачу

Та же схема, что у `LEARN-TASKS.md`: очередь — голые указатели
`VIEW-TASKS.md:NN` в [STATUS-P1.md](./STATUS-P1.md), статусы — таблица ниже,
протокол — `/cm-task-start` и `/cm-task-finish` по
[docs/git-workflow.md](./docs/git-workflow.md). Ветка `p1-<задача>` от
`master`, в `master` через `--no-ff` после зелёного гейта. Гейт для этого
блока: `go build ./... && go vet ./... && go test ./...` **плюс**
`npm --prefix frontend test` — фронтенд меняется в каждой задаче.

Новые задачи дописывать в конец файла, чтобы не сдвигать указатели.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| UI-01 | ✓ DONE (2026-09-24) | internal/session/parser.go, hermes_parser.go, internal/config/types.go, stores/sessions.ts |
| UI-02 | ✓ DONE (2026-09-24) | frontend/src/lib/logGroups.ts, LogStream.svelte, stores/logView.ts, LogEntryRow.svelte |
| UI-03 | ✓ DONE (2026-09-24) | LogStream.svelte, lib/logGroups.ts |
| UI-04 | ✓ DONE (2026-09-24) | internal/session/parser.go, hermes_parser.go, LogStream.svelte |
| UI-05 | ✓ DONE (2026-09-24) | LogStream.svelte, GUI-TESTS.md, docs/design-decisions.md |
| UI-06 | ✓ DONE (2026-09-24) | SessionCard.svelte, locales/{ru,en}/sessionCard.ts |
| UI-07 | ✓ DONE (2026-09-25) | internal/config/types.go, parser.go, hermes_parser.go |
| UI-08 | ✓ DONE (2026-09-25) | parser.go, hermes_parser.go, lib/logGroups.ts |
| UI-09 | ○ TODO | parser.go (stream_event), hermes_parser.go, session.go, manager.go, stores/sessions.ts |
| UI-10 | ○ TODO | lib/toolDisplay.ts, lib/formatters.ts, lib/logGroups.ts |
| UI-11 | ○ TODO | components/ToolCallRow.svelte, LogStream.svelte |
| UI-12 | ○ TODO | components/LiveStatus.svelte, lib/liveStatus.ts, LogStream.svelte |
| UI-13 | ○ TODO | LogStream.svelte, lib/logGroups.ts, stores/sessions.ts |

---

## UI-01: Связать вызов инструмента с его результатом

**Зависит от:** —
**Files:** `internal/config/types.go` (`LogEntry`), `internal/session/parser.go`,
`internal/session/hermes_parser.go`, `frontend/src/stores/sessions.ts`
(`LogEntry`), тесты парсеров.

Сейчас запись `tool` (вызов) и `tool_result` (результат) никак не связаны: у
`rawContent` нет поля `id`, а `rawToolResult.ToolUseID` читается и
выбрасывается. Группировка в UI-02 без этой связи будет угадывать.

**Что сделать.** Добавить в `config.LogEntry` поле `ToolUseID string
json:"tool_use_id,omitempty"`. Заполнять его в Claude-парсере на обеих сторонах:
у `tool_use` — из `id` блока, у `tool_result` (и у `error` с `is_error`) — из
`tool_use_id`. У Hermes-потока id нет, поэтому `hermes_parser.go` сам выдаёт
синтетический id: счётчик на `tool_use`, результат получает id последнего
незакрытого вызова с тем же `Name`. Поле добавить в TS-интерфейс
`LogEntry`.

Колонку в `session_logs` **не добавлять**: история (`History.svelte`) остаётся
как есть. UI-02 умеет работать без id (по соседству записей).

**Готово когда:** тест Claude-парсера — `tool_use` и соответствующий
`tool_result` из одной строки-фикстуры получают одинаковый `ToolUseID`, а два
параллельных вызова — разные. Тест Hermes-парсера — два вызова разных
инструментов и их результаты связаны правильно. Раздел «Stream-JSON Events
(stdout)» в `docs/design-decisions.md` описывает новое поле.

---

## UI-02: Группировка хода агента и режим «Лента»

**Зависит от:** UI-01
**Files:** новый `frontend/src/lib/logGroups.ts`,
`frontend/src/components/LogStream.svelte`, `frontend/src/stores/logView.ts`,
`frontend/src/lib/locales/{ru,en}/logStream.ts`, новый
`frontend/tests/log-groups.spec.ts`.

Ядро блока: превратить плоский массив `LogEntry` в ленту блоков, как у
Hermes, приёмы 1 и 6.

**Что сделать.** Чистая функция `groupEntries(entries: LogEntry[]):
LogBlock[]` в `lib/logGroups.ts`. Типы блоков:

- `user` — реплика пользователя (`level: 'user'`), отдельный пузырь;
- `prose` — реплика агента (`text`) или итог (`result`);
- `tools` — серия подряд идущих вызовов инструментов с их результатами. Пара
  вызов→результат собирается по `tool_use_id`, без него — по соседству.
  Внутри серии записи `system` и `thinking` не рвут группу, `text` и `user`
  рвут;
- `thinking` — размышление (UI-03 даст ему свою строку);
- `other` — `system`, `error`, `cost` и всё незнакомое, выводится как сейчас.

Заголовок свёрнутой группы `tools` строится одной строкой по образцу Hermes:
первая команда или путь (`ToolInput`, обрезать до ~80 символов) и `+ N
команд` / `+ N commands` для остальных. Если в серии только чтения и поиск
(`READ_TOOLS` из `lib/formatters.ts`) — `Просмотрено: a.go, b.go`. Ошибка
внутри группы (`level: 'error'`) видна на свёрнутом заголовке красной
меткой — ошибку нельзя прятать.

Переключатель вида — стор `logLayout: 'feed' | 'classic'` рядом с
`logMarkdown` в `stores/logView.ts`, с тем же хранением в `localStorage`.
**По умолчанию `classic`**: пользователь включает ленту сам, пока блок не
доделан. В `LogStream.svelte` при `feed` — рендер блоков, при `classic` —
сегодняшний `{#each entries}` без изменений.

Раскрытие групп — `Map<number, boolean>` по `seq` первой записи группы.
Раскрытая группа показывает записи в сегодняшнем построчном формате — это
переиспользует существующий код строки. Для этого вынести строку в
компонент `LogEntryRow.svelte`, общий для обоих видов.

**Готово когда:** `log-groups.spec.ts` проверяет: серия из 4 вызовов с
результатами даёт один блок `tools` с заголовком `… + 3`; `text` между
вызовами рвёт серию на две; результат без `tool_use_id` цепляется к
предыдущему вызову; ошибка в серии поднимается в заголовок; поиск по
тексту результата находит группу. Существующие `log-markdown.spec.ts` и
`session.spec.ts` зелёные без правок, то есть классический вид не изменился.

---

## UI-03: Размышления и проза агента

**Зависит от:** UI-02
**Files:** `frontend/src/components/LogStream.svelte` (или новый
`LogFeed.svelte`, если в UI-02 лента вынесена), `frontend/src/lib/logGroups.ts`,
локали, `frontend/tests/log-groups.spec.ts`.

Приёмы 2, 3 и 5 Hermes.

**Что сделать.**

- Блок `thinking` в ленте — одна приглушённая строка `Думал 3s` /
  `Thought for 3s`. Длительность считается как разница `time` этой записи и
  следующей записи сессии и вычисляется в `logGroups.ts` (покрыть тестом).
  Если следующей записи ещё нет (агент думает сейчас) — `Думает…` без
  секунд. Клик раскрывает полный текст курсивом, как сейчас.
- Блоки `prose` (`text`, `result`) — пропорциональный шрифт приложения, не
  `font-mono`, размер 13–14px, межстрочный 1.5, ширина текста не больше
  ~80ch. Markdown рендерится всегда, если включён флажок Markdown: у этих
  уровней он уже включён через `MARKDOWN_LEVELS`. Таблицы и заголовки берут
  стили из глобального `.md-body` (см. «`.md-body` is a global style» в
  `docs/design-decisions.md`) — их не переопределять локально.
- Время `[hh:mm:ss]` в ленте не показывать у каждой строки: оно уходит в
  `title` блока и показывается при наведении.
- Блок `user` — пузырь, выровненный вправо, фон `bg-bg-elevated`, с
  закруглением. Цвет — существующая тема: только классы `text-text*` и
  `bg-bg*` либо пары «светлая база + `dark:`», как требует
  `formatters.spec.ts`.

**Готово когда:** тест длительности размышления: 3 секунды между записями
дают `3s`, последняя запись даёт «думает». Playwright-снимок ленты на
сценарии fakeclaude с текстом, размышлением и серией вызовов (см.
`docs/testing-harness.md`, новый сценарий — запись в
`testdata/scenarios/scenarios_doc.md`) проходит в светлой и тёмной теме.

---

## UI-04: Карточка правки файла с диффом

**Зависит от:** UI-02
**Files:** `internal/session/parser.go` (`AbbreviateInput` и `tool_use`),
`internal/session/hermes_parser.go`, `internal/config/types.go`,
`frontend/src/stores/sessions.ts`, `LogStream.svelte`/`LogFeed.svelte`, тесты.

Приём 4 Hermes: `store_test.go +25` и дифф под ним.

**Что сделать.** Для вызовов правки — Claude `Edit`, `MultiEdit`, `Write`,
Hermes `patch`, `write_file` — парсер кладёт в `LogEntry` новое поле `Diff`
(`json:"diff,omitempty"`): для `Edit` и `patch` — `old_string`/`new_string`
(или то, что Hermes присылает во входе), для `Write` и `write_file` —
содержимое как добавленные строки. Считать `+N −M`. Размер ограничить
(например, 400 строк, дальше «… ещё K строк»), чтобы огромный `Write` не
раздул память стора.

В ленте вызов правки — отдельная карточка, а не строка внутри группы `tools`:
имя файла (путь — в `title`), зелёный `+N` и красный `−M`, по клику — дифф
моноширинным шрифтом, добавленные строки на зелёном фоне, удалённые на
красном. Подсветку синтаксиса не делать.

**Готово когда:** Go-тест: `Edit` с двумя строками в `old_string` и тремя в
`new_string` даёт `Diff` и `+3 −2`; `Write` на 10 строк даёт `+10 −0`;
вход больше лимита обрезается. Hermes-тест на `patch`. Playwright: карточка
показывает счётчики и раскрывается в дифф.

---

## UI-05: Включить ленту по умолчанию и закрыть блок

**Зависит от:** UI-02, UI-03, UI-04
**Files:** `frontend/src/stores/logView.ts`, `GUI-TESTS.md`,
`docs/design-decisions.md`, `docs/architecture.md` (дерево компонентов).

**Что сделать.** Пройти ленту на живых сессиях обоих рантаймов через
cm-mcp/control-plane (`docs/testing-harness.md`) и поправить найденное.
Сценарии: длинная задача разработчика (десятки вызовов), сессия с
ошибкой инструмента, сессия с правками файлов, поиск Ctrl+F по выводу
команды внутри свёрнутой группы. Проверить производительность: лог на
`LOG_BUFFER_LIMIT` записей не должен заметно тормозить при прокрутке и
при приходе новых записей. Автопрокрутка вниз (`pendingStick`/`prevLastSeq`)
работает в ленте так же, как в классическом виде.

После этого сменить значение по умолчанию `logLayout` на `feed`. Если у
пользователя в `localStorage` уже сохранён выбор, он остаётся.

**Готово когда:** в `GUI-TESTS.md` — раздел «LogStream — лента» с
тест-кейсами ленты, отмеченными `✓`; в `docs/design-decisions.md` —
раздел о ленте: зачем она, правила группировки, почему классический вид
сохранён; в `docs/architecture.md` — новые файлы в дереве. Полный гейт
зелёный.

---

## UI-06: Крупная надпись «Use Hermes» / «Use Claude Code» в шапке сессии

**Зависит от:** —
**Files:** `frontend/src/components/SessionCard.svelte`,
`frontend/src/lib/locales/{ru,en}/sessionCard.ts`, новый или существующий
spec в `frontend/tests/`, `GUI-TESTS.md` (раздел «SessionCard.svelte — KPI
Display»).

Пользователь хочет сразу видеть, какой инструмент ведёт сессию. Сейчас это
видно только по мелкому значку `☤` в `Sidebar.svelte` у сессий Hermes, в
шапке сессии — нигде.

**Где.** Карточка сессии над логом (`SessionCard.svelte`), вторая строка:
`Ходов · Токены · Модель: claude-opus-5-5`. Надпись ставится **справа от
модели**, на той же строке, с отступом от неё.

**Что показывать.** `session.runtime === 'hermes'` → **Use Hermes**, иначе
(`''`, отсутствует) → **Use Claude Code**. Поле уже приходит во фронт:
`SessionState.runtime` (`internal/session/manager.go`, `stores/sessions.ts`).
Бэкенд не трогать. Текст надписи не переводить, это название продукта,
но `title` с пояснением («Сессию ведёт Hermes CLI» / «Сессию ведёт Claude Code
CLI») — через локали.

**Как выглядит.** Крупно и заметно на фоне мелкого `text-xs` строки:
`text-lg`–`text-xl`, `font-bold`/`font-extrabold`, с градиентной заливкой
текста (`bg-gradient-to-r … bg-clip-text text-transparent`). Цвета у
инструментов разные и узнаваемые: Hermes — фиолетово-синий, Claude Code —
оранжево-терракотовый в духе фирменного цвета Claude. Надпись должна читаться
в светлой и тёмной теме: градиенты подобрать с `dark:`-вариантами, как
требует `frontend/tests/formatters.spec.ts` для цветов лога. Строка при этом
не должна «прыгать»: выровнять по базовой линии (`items-baseline`) или по
центру, чтобы мелкий текст рядом остался на своём месте, а при узком окне
надпись переносилась вместе с `flex-wrap`, а не обрезала модель.

**Готово когда:** Playwright-тест: карточка сессии с `runtime: 'hermes'`
показывает «Use Hermes», без `runtime` — «Use Claude Code» (данные через
fakeclaude и control-plane, см. `docs/testing-harness.md`; для Hermes —
сессия с `runtime = "hermes"` в тестовом конфиге). Строка в `GUI-TESTS.md`
со `✓`. Снимок экрана в светлой и тёмной теме приложен к итоговому отчёту
задачи.

---

# Живой ход агента: статус, иконки, длительности (UI-07…UI-13)

Пользователь 2026-09-25: «нет никаких динамических действий типа "думает" и
крутится иконка, нет меняющихся строк… вывод не информативный, одни пути к
файлам». Лента UI-01…06 сгруппировала записи, но осталась статичной: не видно,
что агент делает прямо сейчас, инструмент показан одним полем (`ToolInput` —
путь или команда) под общей 🔧, длительностей нет.

## Что у Hermes CLI (по исходникам `NousResearch/hermes-agent`)

Весь рендер — `agent/display.py`. Приёмы, которые переносим:

1. **Живая строка со спиннером** (`KawaiiSpinner`, `display.py:802-937`):
   кадры `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` раз в 0.12 с, справа — прошедшее время.
   Думает: `⠹ (⌐■_■) pondering...  (3.2s)`, глаголы — `THINKING_VERBS`
   (pondering, reasoning, analyzing, synthesizing, …). Работает инструмент:
   `⠼ 💻 Running npm test  (5.2s)` — фраза из `_TOOL_VERBS`
   (`display.py:503-514`): `Reading main.py L10-49`, `Searching the web for
   foo`, `Editing src/x.py`. Пока модель генерирует аргументы —
   `┊ 💻 preparing terminal…`.
2. **Строка завершённого инструмента** (`get_cute_tool_message`,
   `display.py:1106-1203`): `┊ {emoji} {глагол:9} {деталь}  {1.2s}{сбой}`:
   ```
   ┊ 📖 read      main.py L10-49   0.3s
   ┊ 💻 $         npm test + 2 commands   3.4s [exit 1]
   ┊ 🔎 grep      handleEvent   0.1s
   ┊ 🔀 delegate  3x: goalA | goalB
   ```
   Деталь сжимается: shell — `summarize_shell_command` (`display.py:279`)
   выкидывает `cd`/`export`/`source`, редиректы, хвосты `| head|tail|wc|sort`,
   остаток — первая команда + `+ N commands`; файл — basename + диапазон
   строк; пути обрезаются с начала (`…/tail`). Успех — без суффикса, сбой —
   `[exit N]` / `[текст ошибки ≤48]`.
3. **Итог хода и статус:** рамка ответа `╭─ ☤ Hermes ─╮`, строка статуса
   `модель │ 12.3k/200k │ [██░░] 6% │ ⏱ 12s`.

Спиннер у Hermes живёт только в интерактивном терминале; в `--format
stream-json`, которым мы его ведём, приходят только события. Всё живое строим
сами — из событий обоих рантаймов.

## Что у нас теряется (на 2026-09-25)

- **Claude:** `--include-partial-messages` включён, но `stream_event`
  выбрасывается целиком (`parser.go:267`) — а в нём раньше полного сообщения
  приходят `content_block_start{thinking|tool_use,name}`. `AbbreviateInput`
  (`parser.go:558`) оставляет одно поле; `description` у Bash (готовое
  человеческое описание), `offset/limit` у Read, `path/glob` у Grep, `url` у
  WebFetch теряются, у остальных инструментов — сырой JSON.
- **Hermes:** `tool_result.duration_ms` парсится и выбрасывается;
  `tool_call_id` не читается — id синтетические, результат сопоставляется по
  имени (`hermes_parser.go:50-145`).
- **Фронт:** иконки (`lib/formatters.ts:130-191`) знают только имена Claude —
  у Hermes всё 🔧, «Просмотрено:» не срабатывает; вызов и результат в группе
  сопоставлены по соседству, а не по `tool_use_id`; ни одного таймера и
  анимации в ленте; статус сессии остаётся `working` между ходами.

## Инварианты блока (в дополнение к инвариантам выше — они в силе)

- **Разница рантаймов — только в парсерах.** Claude и Hermes нормализуются в
  одни и те же поля `LogEntry` и одно событие активности; фронт не ветвится по
  `runtime`, кроме таблицы имён инструментов в `toolDisplay` (UI-10).
- **Новые поля `LogEntry` — только живые**, как `ToolUseID` и `Diff`: в
  `session_logs` колонок не добавлять, `History.svelte` не меняется. Размер
  ограничивать (аргументы — не больше ~2 КБ на вызов).
- **Классический вид не меняется.** Живая строка, построчные вызовы и итог
  хода — только в ленте.
- **Анимация не должна грузить WebView:** один таймер на видимую ленту (не на
  запись), спиннер — CSS-анимация или смена кадра не чаще 8–10 раз в секунду;
  на неактивной сессии таймеров нет.
- **Чистая логика — в `frontend/src/lib/` с unit-spec'ами** (как
  `logGroups.ts`), в `.svelte` — только рендер.

---

## UI-07: Структурированные аргументы вызова в `LogEntry`

**Зависит от:** —
**Files:** `internal/config/types.go` (`LogEntry`), `internal/session/parser.go`
(`tool_use`, `AbbreviateInput`), `internal/session/hermes_parser.go`
(`tool_use`, `hermesAbbreviateInput`), `frontend/src/stores/sessions.ts`,
тесты парсеров, `docs/design-decisions.md` («Stream-JSON Events (stdout)»).

**Что сделать.** Новое поле `ToolArgs map[string]string
json:"tool_args,omitempty"` — плоские скалярные аргументы вызова (строки,
числа, bool как строки; вложенные объекты/массивы — пропустить или заменить
счётчиком вида `todos: "5 items"`). Каждое значение обрезать до ~300 символов,
весь map — до ~2 КБ. Заполняют оба парсера из `input` у `tool_use`.
`ToolInput` и `AbbreviateInput` остаются как есть — на них стоит классический
вид, поиск и БД.

**Готово когда:** Go-тесты — Claude `Bash` с `command` и `description` даёт оба
ключа; `Read` с `offset/limit` — три ключа; огромный `Write` не раздувает
`ToolArgs` сверх лимита (`content` обрезан); Hermes `terminal` и `read_file`
— их ключи. Поле описано в «Stream-JSON Events (stdout)».

---

## UI-08: Длительность и статус каждого вызова

**Зависит от:** —
**Files:** `internal/config/types.go`, `internal/session/parser.go`,
`internal/session/hermes_parser.go`, `frontend/src/stores/sessions.ts`,
`frontend/src/lib/logGroups.ts`, `frontend/tests/log-groups.spec.ts`, тесты
парсеров.

**Что сделать.**

- Hermes: читать `tool_call_id` из `tool_use`/`tool_result` и класть его в
  `ToolUseID`; синтетический `hermes-N` оставить только как запасной путь, если
  id в событии нет. `duration_ms` из `tool_result` — в новое поле
  `LogEntry.DurationMs int64 json:"duration_ms,omitempty"`.
- Claude: длительности на проводе нет — парсер держит `map[toolUseID]time.Time`
  от `tool_use` и на `tool_result` пишет разницу в `DurationMs` (запись из map
  удалять; map чистить на `result`, чтобы не течь).
- `logGroups.ts`: внутри блока `tools` собрать пары по `tool_use_id` —
  `ToolCall { call, result?, durationMs?, state: 'running'|'ok'|'error' }`.
  Вызов без результата — `running`, пока в группе/после неё не пришёл
  `result` хода (тогда — `ok` без длительности, чтобы оборванный ход не
  «крутился» вечно). Без id — по соседству, как сейчас.

**Готово когда:** Go-тест Hermes — `duration_ms: 312` доходит до записи
результата, id берётся из `tool_call_id`; Claude — два вызова с разным
временем результата получают свои длительности. `log-groups.spec.ts` — пары по
id при перемешанном порядке результатов, `running` у незакрытого, `error` у
`is_error`.

---

## UI-09: Событие активности сессии («думает» / «инструмент X» / «пишет»)

**Зависит от:** —
**Files:** `internal/session/parser.go` (ветка `stream_event`),
`internal/session/hermes_parser.go`, `internal/session/session.go`
(`handleEvent`), `internal/session/manager.go` (ретрансляция во фронт, рядом с
`session:status`), `frontend/src/stores/sessions.ts`, тесты,
`docs/design-decisions.md`, `docs/architecture.md` (если меняется список
событий).

**Что сделать.** Новое непостоянное Wails-событие `session:activity` с
`{id, kind, tool?, since}`, где `kind` — `thinking | tool | writing | idle`,
`since` — время начала текущей активности. В БД не пишется.

- Claude: разобрать `stream_event` вместо выбрасывания —
  `content_block_start` с `thinking` → `thinking`; с `tool_use` и `name` →
  `tool` (это приходит до полного `assistant`-сообщения — аналог
  «preparing terminal…»); с `text` → `writing`; `result` → `idle`. Дельты
  (`*_delta`) не ретранслировать — только смены вида, иначе зальём IPC.
- Hermes: `text` → `writing` (только при смене), `tool_use` → `tool`,
  `tool_result` → `thinking` (модель снова думает), `result` → `idle`; после
  запуска процесса до первого события — `thinking`.
- Статус сессии: после `result` в интерактивной сессии Claude и после конца
  хода Hermes сессия сейчас остаётся `working`. Разобраться, где выставлять
  «ход окончен, ждём ввода», не ломая авто-рестарт и очереди задач (проверить
  по `docs/design-decisions.md`); если это рискованно — ограничиться
  `activity: idle` и описать решение в задаче.

**Готово когда:** Go-тест Claude-парсера на фикстуре со `stream_event`
(thinking → tool_use → text → result) даёт ровно четыре смены активности;
Hermes — аналогично. Стор фронта хранит последнюю активность по id сессии.
fakeclaude умеет слать `stream_event` (если не умеет — добавить, новый
сценарий описать в `testdata/scenarios/scenarios_doc.md`).

---

## UI-10: `toolDisplay` — эмодзи, глагол и человеческое описание вызова

**Зависит от:** UI-07 (без него работает на `tool_input`, с ним — полно)
**Files:** новый `frontend/src/lib/toolDisplay.ts`, новый
`frontend/tests/tool-display.spec.ts`, `frontend/src/lib/formatters.ts`
(`READ_TOOLS`, `logEntryIcon`), `frontend/src/lib/logGroups.ts` (заголовок
группы), `LogStream.svelte`, локали `logStream`.

**Что сделать.** Чистая функция `toolDisplay(entry) → { emoji, verb, detail,
running }`, где `verb` — короткий глагол для строки итога (`read`, `$`,
`grep`), `running` — фраза для живой строки (`Читаю main.go L10-49` /
`Reading main.go L10-49`). Таблица по образцу `_CUTE_LINES`/`_TOOL_VERBS`
Hermes, **для имён обоих рантаймов**:

| Claude | Hermes | emoji | verb | detail |
|---|---|---|---|---|
| Read | read_file | 📖 | read | basename + `L{offset}-{offset+limit}` |
| Write | write_file | ✍️ | write | путь |
| Edit, MultiEdit | patch | 🔧 | edit | путь |
| Bash | terminal | 💻 | $ | `description`, иначе сжатая команда |
| Grep | search_files | 🔎 | grep | pattern (+ `in path`) |
| Glob | search_files (target=files) | 🔎 | find | pattern |
| WebSearch | web_search | 🔍 | search | query |
| WebFetch | web_extract | 📄 | fetch | домен |
| TodoWrite | todo_list | 📋 | plan | `N задач` |
| Agent, Task | delegate_task | 🔀 | delegate | description / goal |
| Skill | skill_view | 📚 | skill | имя |
| `mcp__srv__tool` | — | 🧩 | srv | tool + главный аргумент |
| прочее | прочее | ⚡ | имя | первый из query/text/command/path/name/prompt |

Сжатие shell-команды — порт `summarize_shell_command`: убрать сегменты
`cd`/`export`/`source`/`true`, редиректы, хвосты `| head|tail|wc|sort|uniq`,
несколько оставшихся — первая + `+ N`. Пути длиннее ~60 символов — обрезать с
начала (`…/internal/session/parser.go`). `READ_TOOLS` дополнить именами
Hermes. Заголовок свёрнутой группы в ленте и иконка строки в ленте берутся из
`toolDisplay`; классический вид — без изменений.

**Готово когда:** `tool-display.spec.ts` покрывает каждую строку таблицы для
обоих рантаймов, сжатие команд (`cd x && npm test | tail -5` → `npm test`;
три команды → `… + 2`), обрезку путей, MCP-имя, неизвестный инструмент.
Группа из вызовов Hermes `read_file` даёт «Просмотрено: …».

---

## UI-11: Раскрытая группа — строка на каждый вызов

**Зависит от:** UI-08, UI-10
**Files:** новый `frontend/src/components/ToolCallRow.svelte`,
`LogStream.svelte`, локали, Playwright-spec ленты, `GUI-TESTS.md` (раздел
«LogStream — лента»).

**Что сделать.** Раскрытая группа `tools` показывает не сырые записи, а по
одной компактной строке на `ToolCall` (UI-08):
`📖 read  main.go L10-49 · 0.3s ✓`. Длительность — `0.3s`, `12s`, `1m05s`;
`✓` зелёный, у ошибки — красный `✖` и первые ~60 символов ошибки; у
`running` — маленький спиннер вместо статуса и тикающее время с начала вызова.
Клик по строке раскрывает её до сегодняшних `LogEntryRow` вызова и результата
(полный вывод — как раньше, ни одна запись не теряется). Поиск Ctrl+F,
совпавший с выводом, раскрывает и группу, и строку вызова. Заголовок
свёрнутой группы, пока в ней есть `running`, тоже показывает спиннер.

**Готово когда:** Playwright на сценарии fakeclaude с несколькими вызовами и
одной ошибкой: строки с эмодзи, длительностью и ✓/✖; клик раскрывает вывод;
поиск по тексту вывода находит и раскрывает. Светлая и тёмная тема. Строки в
`GUI-TESTS.md` со `✓`.

---

## UI-12: Живая строка статуса внизу ленты

**Зависит от:** UI-09, UI-10
**Files:** новый `frontend/src/components/LiveStatus.svelte`, новый
`frontend/src/lib/liveStatus.ts` (+ spec), `LogStream.svelte`, локали,
`GUI-TESTS.md`.

**Что сделать.** Под последним блоком ленты, пока активность сессии не
`idle` (UI-09) и сессия запущена, — одна строка, прилипающая к низу при
автопрокрутке:

- `thinking`: `⠹ Размышляю… · 4s` — глагол меняется раз в несколько секунд из
  локализованного списка (ru: размышляю, анализирую, прикидываю, сопоставляю,
  формулирую…; en: pondering, reasoning, analyzing, synthesizing…);
- `tool`: `⠼ 💻 Запускаю npm test · 12s` — фраза `running` из `toolDisplay`
  по последнему незакрытому вызову, до прихода вызова — `💻 Готовлю Bash…`
  по имени из активности;
- `writing`: `⠧ ✍ Пишет ответ… · 2s`;
- ожидание разрешения/вопроса — не дублировать, там свои баннеры.

Спиннер — кадры `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`, время считается от `since`. Выбор текста —
чистая функция в `liveStatus.ts`. Блок `thinking` в ленте без следующей записи
(«Думает…», UI-03) тоже показывает тикающие секунды — от того же таймера.

**Готово когда:** spec на `liveStatus.ts` (каждый `kind`, смена глагола,
формат времени); Playwright — в работающей fakeclaude-сессии строка видна и
меняется с «думаю» на инструмент, после `result` исчезает. Проверено, что
неактивная/скрытая сессия не держит таймеров.

---

## UI-13: Итог хода

**Зависит от:** UI-09
**Files:** `LogStream.svelte`, `frontend/src/lib/logGroups.ts` (+ spec),
`frontend/src/stores/sessions.ts`, локали, `GUI-TESTS.md`,
`docs/design-decisions.md` (раздел о ленте).

**Что сделать.** После блока `prose` с `result` — приглушённая строка итога
хода: `✓ Готово за 42s · 12 ходов · 35k токенов · $0.18` (у Hermes — что
есть: длительность и токены; стоимость — только если известна). Неуспех —
красный `✖` и подтип/ошибка. Данные уже приходят в `session:result`
(`duration_ms`, `num_turns`, cost, tokens) — привязать их к записи `result` в
сторе (по времени/последнему `result`), не меняя БД. В разделе о ленте в
`docs/design-decisions.md` описать весь блок UI-07…13: источник активности
для каждого рантайма, таблицу `toolDisplay`, почему поля живые.

**Готово когда:** Playwright — после завершения хода fakeclaude строка итога
видна с длительностью и числом ходов; сессия Hermes — с длительностью и
токенами. Полный гейт зелёный, `GUI-TESTS.md` обновлён.
