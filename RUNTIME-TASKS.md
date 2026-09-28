# Единый формат событий рантаймов — план работ (RT-01…RT-08)

Менеджер запускает сессии на двух рантаймах — Claude Code (`claude -p
--output-format stream-json`) и Hermes (`hermes chat --format stream-json`).
У каждого свой поток событий и своё поведение. Цель блока: всё, что делает
рантайм, приводится к одному внутреннему формату, а ядро менеджера (статусы,
вопросы, ошибки, лента, метрики) работает только с ним и одинаково для любого
рантайма. Новый рантайм (Codex, Kimi…) тогда подключается адаптером, не
трогая ядро.

## Откуда задача (инцидент 2026-09-28)

В сессии `Lumen browser/Chat` (Hermes) агент вызвал инструмент `clarify` с
тремя вопросами. Headless `hermes chat` сам ответил на него «no user
available… pick the best option», агент продолжил с собственным выбором, а
пользователь вопроса так и не увидел. Исправлено коммитом `2064fe0`: адаптер
Hermes кладёт вопросы в `ParsedEvent.Clarify`, `hermesRunTurn` обрывает ход,
вопросы идут в `QuestionBanner`, ответы продолжают беседу через `--resume`
(см. `docs/design-decisions.md`, «`clarify` reaches the user»).

Вывод из инцидента: общий формат уже есть (`ParsedEvent` в
`internal/session/parser.go`, общий `handleEvent`), но он описывает только
«строку лога / вызов инструмента / результат». Смысловые вещи — «агент задал
вопрос», «кончился лимит», «ошибка авторизации» — каждый рантайм сообщает
по-своему, и ядро ловит их в разных местах: маркер ask-user в `handleEvent`,
`clarify` в `hermes_runtime.go`, лимиты в `drainStderr`/`drainHermesStderr`/
`agent.log`. Где-то остаются дыры: `AskUserQuestion` у Claude Code сейчас не
обрабатывается вообще.

## Как устроено сейчас (отправная точка)

```
claude stdout ──ParseLine (parser.go)──────────┐
                                                ├─> []ParsedEvent ─> Session.handleEvent ─> SessionEvent ─> Manager ─> Wails ─> UI
hermes stdout ──hermesStream.Parse (hermes_parser.go)┘
```

- Цикл запуска разный: `runOnce` (`session.go`, один долгий процесс на
  задачу, ввод — stream-json по stdin) и `runOnceHermes`
  (`hermes_runtime.go`, процесс на каждый ход, беседа — через `--resume`).
- Флаги исхода хода (`rateLimited`, `authErrorHit`, `sessionNotFoundHit`,
  `contextRestartHit`, `continueMarkerHit`, `stepLimitHit`) — атомики в
  `Session`, выставляются из парсеров, stderr-дренажей и `handleEvent`.
- HR-01 в `HERMES-TASKS.md` сознательно отложил интерфейс `Runtime` «до
  третьего рантайма» — этот блок его и делает.

## Инварианты блока

- **Поведение Claude-сессий не меняется**, пока задача прямо не говорит
  обратное. Существующие тесты `internal/session`, e2e-сценарии fakeclaude и тесты
  аргументов `claude` (`TestBuildCLIArgs_*`) проходят **без правок**.
- **Документация — часть задачи, а не хвост.** Каждая задача в том же
  коммите обновляет [docs/runtimes.md](./docs/runtimes.md) (создаётся в
  RT-01): что поменялось в формате, кто что выдаёт, как это проверить.
  Задача без обновлённого `docs/runtimes.md` не считается готовой. Плюс
  обычная матрица doc-sync из `CLAUDE.md` (новое поле события →
  `docs/design-decisions.md`, «Stream-JSON Events»; новый файл в `internal/`
  → дерево в `docs/architecture.md`; новый сценарий fake-рантайма →
  `testdata/scenarios/scenarios_doc.md`).
- **Ядро не знает имён рантаймов.** Новая логика в `handleEvent`/общем цикле
  не проверяет `IsHermes()`; различие живёт в адаптере.
- **Ни одного вызова настоящего API в обычном `go test`.** Реальные
  `claude`/`hermes` — только opt-in тестами (`CM_REAL_CLAUDE=1`,
  `CM_REAL_HERMES=1`), по образцу `hermes_real_test.go`.
- **Записи реальных потоков — в `testdata/`** (`testdata/hermes-stream/`,
  новый `testdata/claude-stream/`), парсерные тесты читают их, а не
  выдуманные строки.

## Как сессия берёт задачу

Та же схема, что у `VIEW-TASKS.md`: очередь — голые указатели
`RUNTIME-TASKS.md:NN` в [STATUS-P1.md](./STATUS-P1.md), статусы — таблица
ниже, протокол — `/cm-task-start` и `/cm-task-finish` по
[docs/git-workflow.md](./docs/git-workflow.md). Ветка `p1-<задача>` от
`master`, в `master` через `--no-ff` после зелёного гейта. Гейт:
`go build ./... && go vet ./... && go test ./...`; если задача трогает
фронтенд — плюс `npm --prefix frontend test`.

Задачи идут по порядку: каждая опирается на предыдущие. Новые задачи
дописывать в конец файла, чтобы не сдвигать указатели.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| RT-01 | ✓ DONE (2026-09-28) | docs/runtimes.md, docs/architecture.md |
| RT-02 | ✓ DONE (2026-09-28) | internal/session/session_realclaude_test.go, testdata/claude-stream/, docs/runtimes.md |
| RT-03 | ✓ DONE (2026-09-28) | parser.go, hermes_parser.go, session.go, hermes_runtime.go, cmd/fakeclaude, docs/runtimes.md |
| RT-04 | ✓ DONE (2026-09-28) | parser.go, hermes_parser.go, session.go, hermes_runtime.go, ratelimit.go, turn_failure.go, docs/runtimes.md |
| RT-05 | ✓ DONE (2026-09-28) | internal/session/runtime.go, runtime_claude.go, runtime_hermes.go |
| RT-06 | ○ TODO | session.go, hermes_runtime.go, runtime*.go |
| RT-07 | ○ TODO | internal/session/runtime_contract_test.go, cmd/fakeclaude, cmd/fakehermes |
| RT-08 | ○ TODO | docs/runtimes.md, HERMES-TASKS.md, CLAUDE.md |

---

## RT-01: `docs/runtimes.md` — как рантаймы приводятся к общему формату

**Зависит от:** —
**Files:** `docs/runtimes.md` (новый), `CLAUDE.md` (карта документации),
`docs/architecture.md` (ссылка из раздела про `internal/session`).

**Что сделать.** Описать **текущее** устройство, ничего не меняя в коде —
это база, которую следующие задачи будут дополнять:

1. Схема потока: stdout рантайма → адаптер → `ParsedEvent` → `handleEvent` →
   `SessionEvent` → `Manager` → Wails-события → сторы Svelte.
2. Таблица `ParsedEvent`: каждое поле, что оно значит, кто его заполняет
   (Claude / Hermes / оба), какое `SessionEvent` из него получается.
3. Таблица соответствия событий: строка потока Claude → `ParsedEvent`, строка
   потока Hermes → `ParsedEvent` (включая синтетические id вызовов у Hermes,
   буферизацию `text`-дельт, `todo_list` → TaskPanel, `clarify` →
   `ParsedEvent.Clarify`).
4. Жизненный цикл хода и задачи для каждого рантайма: один процесс на
   задачу против процесса на ход; как доставляется ввод (`inputCh` →
   stdin-конверт / `--resume`); что считается концом хода и концом задачи
   (ссылка на HR-04a).
5. Все места, где сейчас определяется исход хода (лимит, 401/403, session
   not found, контекст, step limit, ask-user, `clarify`) — файл, функция,
   какой атомик выставляется. Это список долга для RT-03/RT-04.
6. Известные различия рантаймов, которые **нельзя** свести форматом, только
   адаптером (пример — самоответ `clarify` в headless Hermes).
7. Раздел «Как добавить рантайм» — пока черновой чек-лист по текущему коду;
   RT-08 доводит его до финального.

**Готово когда:** `docs/runtimes.md` есть, в `CLAUDE.md` строка в карте
документации, каждое утверждение документа проверено по коду (указаны
`файл:функция`). Код не менялся, гейт зелёный.

---

## RT-02: Как Claude Code headless обрабатывает `AskUserQuestion`

**Зависит от:** RT-01
**Files:** `internal/session/session_realclaude_test.go` (новый тест
рядом с существующим, тот же opt-in `CM_REAL_CLAUDE=1`), `testdata/claude-stream/ask-user-question.jsonl`
(запись), `docs/runtimes.md`.

**Что сделать.** Выяснить на настоящем `claude` (с теми же флагами, что
строит `buildCLIArgs`: `-p --input-format stream-json --output-format
stream-json --verbose`, интерактивная и `bypassPermissions`-сессия), что
происходит, когда модель вызывает `AskUserQuestion`: приходит ли
`tool_use`, что в `tool_result` (ошибка? автоответ? ожидание?), есть ли
`permission_request`, как на это реагирует модель. Промпт — короткий и
дешёвый (Haiku, «задай мне вопрос через AskUserQuestion с двумя
вариантами»). Записать сырой поток в `testdata/claude-stream/`, описать
поведение в `docs/runtimes.md` и выбрать способ доставить ответ
пользователя: stdin-сообщение после `tool_result`, ответ через
`permission_response`, прерывание хода — с обоснованием.

**Готово когда:** запись в `testdata/claude-stream/`, opt-in тест её
воспроизводит, в `docs/runtimes.md` — раздел «AskUserQuestion у Claude Code»
с выводом и выбранным способом ответа. Обычный гейт зелёный (opt-in тест в
нём пропускается).

---

## RT-03: Единое событие «вопрос пользователю»

**Зависит от:** RT-02
**Files:** `internal/session/parser.go`, `hermes_parser.go`, `session.go`,
`hermes_runtime.go`, `cmd/fakeclaude` (+ сценарий, `scenarios_doc.md`),
`docs/runtimes.md`, `docs/design-decisions.md`.

**Что сделать.** Заменить три разных пути вопросов одним:

- В `ParsedEvent` — поле `Questions []Question` (`Text`, `Choices`,
  `MultiSelect`, `Source`: `ask_user_marker` | `hermes_clarify` |
  `claude_ask_user_question`) вместо `Clarify []ClarifyQuestion`.
- Адаптеры заполняют его: Hermes — из `clarify` (как сейчас), Claude — из
  `AskUserQuestion` (по выводам RT-02), оба — из маркера ```` ```ask-user ````
  в тексте результата (разбор маркера переезжает из `handleEvent` в
  адаптеры/общий хелпер).
- Ядро: один механизм набора вопросов (сейчас `clarifyState` +
  `askClarify`/`openClarifyQuestion` в `hermes_runtime.go`) переносится в
  общий код `session.go`: вопросы по одному через `PendingQuestion`, ответы
  собираются, отправляются одним сообщением. Как доставить ответ и надо ли
  обрывать ход — решает рантайм (Hermes: оборвать и `--resume`; Claude — по
  RT-02).
- `KindContinueSession` (автоответ «нет» на вопрос о продолжении сессии) и
  таймаут первого варианта только для автономных прогонов — сохраняются.
- Маркер ask-user остаётся только для автономных прогонов (как сейчас),
  `clarify`/`AskUserQuestion` — в любых.

**Готово когда:** все три источника дают баннер вопросов и ответ доходит до
агента; тесты на каждый источник (парсер + полный цикл на fakeclaude и
fakehermes); существующие тесты ask-user (`session_askuser_test.go`)
проходят без правок; `docs/runtimes.md` — раздел «Вопросы пользователю» с
таблицей источников и способов доставки ответа.

---

## RT-04: Исход хода как событие, а не россыпь атомиков

**Зависит от:** RT-03
**Files:** `internal/session/parser.go`, `hermes_parser.go`, `session.go`,
`hermes_runtime.go`, `task_outcome.go`, `ratelimit.go`, `docs/runtimes.md`.

**Что сделать.** Ввести в общий формат классифицированную ошибку/исход:
`ParsedEvent.Failure *TurnFailure{Kind, Message, ResetsAt}` с `Kind` из
`rate_limit` | `auth` | `session_not_found` | `context_restart` |
`step_limit` | `other`. Классификацию делает адаптер (включая stderr — его
строки тоже идут через адаптер, и `agent.log` Hermes). Ядро выставляет
состояние исхода в одном месте по этому событию; `runOnce`/`runOnceHermes`
читают его, а не шесть атомиков по отдельности. Текущие сообщения в логе и
паузы (`rate_limit_pause`, 60 с при auth) не меняются.

**Готово когда:** каждый `Kind` покрыт тестом для обоих рантаймов (где у
рантайма такой исход бывает), тесты rate limit/auth/401-за-429 проходят
без правок, `docs/runtimes.md` — таблица «исход → кто определяет → что
делает ядро».

---

## RT-05: Интерфейс `Runtime`

**Зависит от:** RT-04
**Files:** `internal/session/runtime.go` (интерфейс),
`runtime_claude.go`, `runtime_hermes.go` (перенос из `session.go` /
`hermes_runtime.go`), `docs/runtimes.md`, `docs/architecture.md` (дерево).

**Что сделать.** Выделить то, чем рантаймы различаются, за интерфейс
(набросок — HR-01 в `HERMES-TASKS.md`, уточнить по итогам RT-03/04):
аргументы запуска, окружение, как отдать промпт/ответ/картинки, парсер
stdout и stderr, процесс на задачу или на ход, как продолжить беседу
(`--resume` id), что делать при вопросе пользователю. `claudeRuntime` и
`hermesRuntime` — реализации; выбор по `Config.Runtime` в одном месте.
Циклы `runOnce`/`runOnceHermes` пока остаются двумя, но работают через
интерфейс.

**Готово когда:** весь существующий набор тестов зелёный без правок,
тесты `TestBuildCLIArgs_*` не изменились, в `docs/runtimes.md` —
описание интерфейса с комментарием к каждому методу.

---

## RT-06: Общий цикл хода

**Зависит от:** RT-05
**Files:** `internal/session/session.go`, `hermes_runtime.go`,
`runtime*.go`, `docs/runtimes.md`.

**Что сделать.** Свести `runOnce` и `runOnceHermes` в один цикл поверх
`Runtime`: подготовка промпта (преамбула, картинки), запуск, чтение
событий, ожидание ввода между ходами, вопросы, исход хода, конец задачи.
Различие «один процесс на задачу / процесс на ход» — свойство рантайма, а
не ветка в цикле. Убрать продублированный код (сброс атомиков, ожидание
`inputCh`, обработка исходов).

**Готово когда:** один цикл, оба рантайма через него, весь набор тестов
(включая e2e fakeclaude и `hermes_runtime_test.go`) зелёный без правок;
`docs/runtimes.md` — схема цикла хода.

---

## RT-07: Контрактные тесты адаптеров

**Зависит от:** RT-06
**Files:** `internal/session/runtime_contract_test.go` (новый),
`cmd/fakeclaude`, `cmd/fakehermes` (недостающие сценарии),
`testdata/scenarios/scenarios_doc.md`, `docs/runtimes.md`.

**Что сделать.** Один набор тестов, который прогоняется на каждом
рантайме (табличный тест по списку `Runtime`) и проверяет контракт: init с
id беседы; пара вызов/результат с общим id; длительность вызова; текст
агента; вопрос пользователю и доставка ответа; конец хода с токенами;
каждый `Kind` исхода из RT-04, который рантайм умеет; продолжение беседы
после перезапуска. Для этого fake-рантаймы получают одинаковые сценарии по
ключевым словам. Новый рантайм обязан пройти этот набор — это и есть
определение «рантайм подключён».

**Готово когда:** контрактный набор зелёный на обоих рантаймах; в
`docs/runtimes.md` — список пунктов контракта и как добавить рантайм в
набор.

---

## RT-08: Закрыть блок

**Зависит от:** RT-07
**Files:** `docs/runtimes.md`, `HERMES-TASKS.md` (HR-01 → ссылка на RT-05),
`CLAUDE.md`, `docs/design-decisions.md`.

**Что сделать.** Довести раздел «Как добавить рантайм» в
`docs/runtimes.md` до пошаговой инструкции: какие файлы создать, какой
интерфейс реализовать, какой fake-рантайм написать, какие записи потока
положить в `testdata/`, как пройти контрактный набор, что обновить в
конфиге и UI (выбор рантайма в карточке сессии). Проверить, что документ
соответствует коду после RT-02…07. В `HERMES-TASKS.md` отметить HR-01 как
сделанный в RT-05. В `docs/design-decisions.md` — раздел «Why a common
event format» со ссылкой на инцидент и на `docs/runtimes.md`.

**Готово когда:** человек (или агент), не видевший этот блок, по
`docs/runtimes.md` может добавить третий рантайм; все ссылки из
`CLAUDE.md`/`docs/architecture.md`/`HERMES-TASKS.md` ведут в актуальные
разделы. Гейт зелёный.
