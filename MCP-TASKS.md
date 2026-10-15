# MCP-сервер Claude Manager: полное управление для сторонних агентов — план работ (MCP-01…MCP-17)

Цель блока: `cm-mcp` перестаёт быть внутренним тестовым мостом и становится
**публичным интерфейсом с полным управлением менеджером** — MCP-сервером по
текущей спецификации Model Context Protocol. Всё, что пользователь делает
в GUI, сторонний агент (Claude Code, Claude Desktop, Cursor, Codex,
Hermes…) может сделать через MCP — без чтения нашего кода: по
`instructions` из `initialize`, описаниям инструментов, схемам входа/выхода,
ресурсам-справкам и `docs/mcp.md`.

Два критерия готовности блока:

1. **Паритет.** Каждый экспортированный метод `*App` (Wails-биндинги — это
   и есть полный список действий GUI) доступен через MCP-инструмент либо
   стоит в списке исключений с причиной. Проверяется тестом (MCP-03), и
   тест остаётся в гейте навсегда — новый метод GUI без MCP не сольётся.
2. **Понятность.** В MCP-16 модель, не видевшая репозиторий, с одним только
   подключённым `cm-mcp` с нуля настраивает проект и сессию, запускает
   работу, отвечает на вопросы и permissions, разбирает ошибки и расходы —
   без лишних вызовов и без ошибок аргументов.

## Как устроено сейчас (отправная точка)

```
агент ──stdio JSON-RPC──> cmd/cm-mcp (main.go, самописный) ──HTTP /rpc,/wait + token──> internal/control (server.go, rpc.go) ──> session.Manager / App
                                       └ список инструментов: internal/control/mcptools.go (MCPTools)
```

Три слоя разной ширины (на 2026-10-13):

| Слой | Где | Методов |
|---|---|---|
| GUI — Wails-биндинги `*App` | `app.go`, `app_*.go` | 87 |
| control-plane RPC | `internal/control/rpc.go:buildRegistry` | 34 |
| MCP-инструменты | `internal/control/mcptools.go:buildMCPTools` | 19 |

Методы `App`, которых **нет даже в RPC** (53) — по областям:

- **Сессии:** `StartSessionWithModel`, `StartAdHocChatSession`,
  `SetSessionModel`, `SetSessionRuntime`, `SendMessageWithImages`.
- **Взаимодействие:** `AnswerQuestion`, `GetPendingQuestions`,
  `AddPermissionRule`, `GetPermissionCandidates`.
- **Проекты и конфиг:** `GetProjects`, `InitGitRepo`, `HasClaudeMd`,
  `GenerateClaudeMdSession`, `InstallSessionProtocol`, `MirrorProject`,
  `UnmirrorProject`, `SyncProjectMirrors`, `GetProjectMirror`.
- **Логи:** `ExportLog`, `ImportProjectLogs`, `ClearProjectLogs`,
  `CleanOldLogs`, `GetProjectLogFiles`.
- **Метрики и оптимизация:** `GetDailyTokens`, `GetProjectTokens`,
  `GetTokenAttribution`, `GetTopActions`, `GetActionSamples`,
  `GetDurationProfile`, `GetModelRecommendation`, `GetAutoModelRouting`.
- **Roadmap:** `GenerateRoadmap`, `ApproveRoadmap`, `GetLatestDraftRoadmap`,
  `GetSessionRoadmap`, `GetRoadmapRowDetail`, `GetRoadmapTaskDetail`.
- **Навыки (experience layer):** `GetSkills`, `GetSkillCandidates`,
  `ApproveSkill`, `DistillSkill`, `ArchiveSkill`, `RestoreSkill`,
  `GetSkillQuality`, `GetSkillUsage`, `GetSkillAutopilot`,
  `SetSkillAutopilot`, `RunSkillAutopilot`.
- **Окно ОС (кандидаты в исключения):** `PickDirectory`, `MinimizeToTray`,
  `ShowMainWindow`, `Notify`.

Остальное:

- `cmd/cm-mcp/main.go` — ручной разбор JSON-RPC: `initialize` (жёстко
  `protocolVersion: "2024-11-05"`, только capability `tools`), `tools/list`,
  `tools/call`. Нет согласования версии, `ping`, отмены, progress,
  `instructions`, ресурсов, промптов, `outputSchema`/`structuredContent`,
  аннотаций, пагинации.
- Схемы инструментов — JSON-строки руками, описания в одну строку.
  Идентификация сессии непоследовательна: `project`+`session` или
  `session_id` (`"project/session"`).
- Конфиг проектов и сессий GUI меняет целиком через `GetConfig` /
  `UpdateConfig` — для агента это неудобно и опасно (MCP-08 даёт точечные
  операции).
- Control-plane включён по умолчанию (`CM_CONTROL=0` выключает), адрес и
  токен — в `~/.claude-manager/control.json` (`endpoint.go`).
- Документация инструментов — таблица в `docs/testing-harness.md`
  («cm-mcp tools available to Claude»), рассчитанная на GUI-тесты.

## Инварианты блока

- **Паритет с GUI.** Метод `*App` без MCP-инструмента допустим только в
  списке исключений (`excluded`: имя → причина, например «оконное
  действие ОС», «заменено аргументом `path`») или во временном списке
  `pending` (метод ещё не перенесён). `pending` только сокращается; к
  MCP-17 он пуст. Тест паритета (MCP-03) входит в обычный `go test`.
- **Совместимость.** Существующие имена инструментов и аргументов работают
  до конца блока: переименование — через алиас с пометкой `deprecated` в
  описании, удаление алиасов — отдельной задачей после MCP-17. Тесты
  GUI/Playwright и `cmd/cm-mcp/main_test.go` проходят без правок смысла.
- **Одна точка правды.** Имя, описание, схемы, аннотации, уровень доступа и
  привязка к методу `App` объявляются в одном месте (реестр в Go);
  `tools/list`, `docs/mcp.md` и тест паритета получают их оттуда.
- **GUI и MCP — один код.** Инструмент вызывает тот же метод `App` (через
  RPC control-plane), что и кнопка в GUI; своей бизнес-логики в
  `cm-mcp`/`internal/control` нет. Действие через MCP видно в GUI так же,
  как сделанное руками (те же Wails-события).
- **Безопасность по умолчанию.** Уровни доступа (MCP-05) вводятся **до**
  массового добавления инструментов. Всё, что останавливает чужую работу,
  удаляет данные, меняет конфиг, одобряет permission/план/навык вместо
  человека, — уровень выше режима по умолчанию и включается явно. Ни один
  инструмент не убивает/не перезапускает `claude-manager.exe` (см. «Known
  gotchas» в `CLAUDE.md`). Токен control-plane и секреты конфига не
  попадают ни в ответы, ни в логи. Инвариант `LEARN-TASKS.md` «ничего не
  пишется в репозиторий пользователя без одобрения» распространяется на
  одобрение через MCP: оно доступно только на уровне `admin`.
- **Вывод экономен.** Любой ответ ограничен по размеру и пагинируется
  курсором; агенту сообщается, как получить продолжение (причина —
  стоимость контекста, `docs/Science_lab/MEASURE.md`).
- **Ни одного вызова настоящего API в обычном `go test`.** Оценка реальной
  моделью (MCP-16) — только opt-in (`CM_MCP_EVAL=1`).
- **Документация — часть задачи.** Каждая задача в том же коммите обновляет
  `docs/mcp.md` (создаётся в MCP-01) и обычную матрицу doc-sync из
  `CLAUDE.md`; новый RPC-метод — ещё и строка в таблице Wails Bindings
  `docs/architecture.md`, если меняется сигнатура `App`.

## Шаблон «доменной» задачи (MCP-06…MCP-11)

Каждая задача переноса области делает для **каждого** метода `App` своей
области:

1. RPC-метод в `internal/control/rpc.go` (если его нет), вызывающий метод
   `App`/`Manager` без собственной логики; тест RPC на `fakeclaude`.
2. MCP-инструмент в реестре по стандарту MCP-03/04: схемы входа/выхода,
   `title`, аннотации, уровень доступа, описание по шаблону, действенные
   ошибки, лимит ответа.
3. Удаление метода из `pending` (или перенос в `excluded` с причиной).
4. Тест инструмента через `cm-mcp` → control-plane → `fakeclaude`.
5. Раздел области в `docs/mcp.md` + обновление golden `tools/list`.

Несколько тонких методов `App` можно сводить в один инструмент (например,
`get_cost` с параметром `scope`), если это проще агенту; привязка в
реестре тогда указывает все покрытые методы.

## Как сессия берёт задачу

Та же схема, что у `RUNTIME-TASKS.md`: очередь — голые указатели
`MCP-TASKS.md:NN` в [STATUS-P1.md](./STATUS-P1.md), статусы — таблица ниже,
протокол — `/cm-task-start` и `/cm-task-finish` по
[docs/git-workflow.md](./docs/git-workflow.md). Ветка `p1-<задача>` от
`master`, в `master` через `--no-ff` после зелёного гейта. Гейт:
`go build ./... && go vet ./... && go test ./...`; если задача трогает
фронтенд — плюс `npm --prefix frontend test`.

Задачи идут по порядку: каждая опирается на предыдущие. Новые задачи
дописывать в конец файла, чтобы не сдвигать указатели.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| MCP-01 | ✓ DONE (2026-10-15) | docs/mcp.md, docs/design-decisions.md |
| MCP-02 | ○ TODO | cmd/cm-mcp/, go.mod |
| MCP-03 | ○ TODO | internal/control/mcptools.go → реестр, тест паритета |
| MCP-04 | ○ TODO | реестр, internal/control/rpc.go, тест-линтер |
| MCP-05 | ○ TODO | cmd/cm-mcp/, internal/control/ |
| MCP-06 | ○ TODO | сессии: rpc.go, реестр |
| MCP-07 | ○ TODO | вопросы, permissions, планы: rpc.go, реестр |
| MCP-08 | ○ TODO | проекты, конфиг, git, mirror: rpc.go, реестр, internal/config |
| MCP-09 | ○ TODO | логи и метрики: rpc.go, реестр |
| MCP-10 | ○ TODO | preflight, roadmap, mixed: rpc.go, реестр |
| MCP-11 | ○ TODO | навыки: rpc.go, реестр |
| MCP-12 | ○ TODO | ожидание и события: cmd/cm-mcp/, internal/control/wait.go, emit.go |
| MCP-13 | ○ TODO | ресурсы и промпты: cmd/cm-mcp/ |
| MCP-14 | ○ TODO | conformance и e2e: cmd/cm-mcp/*_test.go, testdata/scenarios/ |
| MCP-15 | ○ TODO | docs/mcp.md, README.md, CLAUDE.md, docs/testing-harness.md |
| MCP-16 | ○ TODO | testdata/mcp-eval/, раннер оценки |
| MCP-17 | ○ TODO | docs/mcp.md, docs/design-decisions.md, CLAUDE.md |

---

## MCP-01: Аудит, карта паритета и решения — `docs/mcp.md` + ADR

**Зависит от:** —
**Files:** `docs/mcp.md` (новый), `docs/design-decisions.md`, `CLAUDE.md`
(проверить строку карты документации).

**Что сделать.** Код не меняется. Зафиксировать решения, на которые
опираются все следующие задачи:

1. **Версия спецификации.** Взять актуальную стабильную ревизию с
   modelcontextprotocol.io (на момент написания блока — `2025-11-25`;
   проверить, не вышла ли новее) и выписать, что сервер обязан
   поддержать: lifecycle и согласование версии, `ping`, отмена, progress,
   `tools` (с `outputSchema`, `structuredContent`, `annotations`, `title`),
   `resources` (+ templates, подписки), `prompts`, `logging`, пагинация,
   разделение ошибок протокола и ошибок инструмента. Что сознательно
   **не** делаем (sampling, elicitation, Streamable HTTP — или делаем) —
   с обоснованием.
2. **SDK или своё.** Официальный Go SDK
   (`github.com/modelcontextprotocol/go-sdk`) против доводки самописного
   `main.go`: чистый Go без CGO (требование проекта), зрелость, генерация
   схем из структур, поддержка версии из п.1. Рекомендация по умолчанию —
   официальный SDK, если покрывает п.1.
3. **Модель идентификаторов.** Один способ адресовать сессию во всех
   инструментах (рекомендация: `session_id = "project/session"`), проект —
   `project`. Таблица «старый аргумент → новый», алиасы на время блока.
4. **Карта паритета** — главный артефакт задачи. Таблица по **всем**
   экспортированным методам `*App` (сейчас 84): метод → область (задача
   MCP-06…11) → имя MCP-инструмента → уровень доступа (`read` / `operate` /
   `admin`) → аннотации (только чтение / разрушительное / идемпотентное) →
   или `excluded` с причиной. Отдельно проверить фронтенд: есть ли
   действия GUI, которые идут **мимо** методов `App` (прямые Wails-события,
   состояние только во фронтенде — например, переключатели вида). Каждое
   такое действие либо признаётся чисто визуальным (исключение), либо
   переносится на бэкенд в соответствующей доменной задаче.
5. **Уровни доступа** (детали — MCP-05): определение уровней, режим по
   умолчанию (рекомендация — `operate`), чем `admin` защищён.
6. Раздел «Why cm-mcp is a public interface with full GUI parity» в
   `docs/design-decisions.md`: зачем, инварианты блока, ссылка на
   `docs/mcp.md`.

**Готово когда:** `docs/mcp.md` содержит разделы «Спецификация», «SDK»,
«Идентификаторы», «Уровни доступа», «Карта паритета» (все методы `App`, ни
одной пустой ячейки) с проверенными по коду ссылками `файл:функция`; ADR в
`design-decisions.md`; гейт зелёный.

---

## MCP-02: Протокольный слой по спецификации

**Зависит от:** MCP-01
**Files:** `cmd/cm-mcp/main.go` (+ новые файлы рядом), `go.mod`/`go.sum`
(если выбран SDK), `cmd/cm-mcp/main_test.go`.

**Что сделать.** Перевести транспорт и жизненный цикл на решение MCP-01, **Поправка MCP-01: см. «Поправки по итогам MCP-01» в конце файла — целевая ревизия `2026-07-28`, сервер dual-era.**
**не меняя набор и поведение инструментов**:

- согласование `protocolVersion` (клиент просит старую поддерживаемую —
  отвечаем ей, иначе — нашей последней), `notifications/initialized`,
  `ping`, `notifications/cancelled`;
- `serverInfo` с `name`, `title`, `version` (версия — из сборки);
- поле `instructions` в ответе `initialize`: 10–20 строк для агента — что
  такое менеджер, формат `session_id`, жизненный цикл статусов, «жди через
  `wait_for_*`, не опрашивай в цикле», где справка (ресурс `cm://guide`
  появится в MCP-13, до того — ссылка на `docs/mcp.md`);
- stdout — только протокол, диагностика — в stderr;
- недоступный control-plane (`control.json` нет, приложение не запущено) —
  понятная ошибка инструмента с подсказкой «запустите Claude Manager», а
  не падение сервера.

**Готово когда:** тест прогоняет handshake для двух версий протокола,
`ping`, отмену и вызов при недоступном control-plane; существующие тесты
зелёные; раздел «Протокол» в `docs/mcp.md`.

---

## MCP-03: Реестр инструментов и тест паритета

**Зависит от:** MCP-02
**Files:** `internal/control/mcptools.go` (переработка), `cmd/cm-mcp/`,
`testdata/mcp/tools.golden.json`, тест паритета; `docs/architecture.md`
(дерево, если появятся файлы); `CLAUDE.md` (матрица doc-sync).

**Что сделать.**

- Каждый инструмент объявляется один раз: Go-структуры входа и выхода с
  тегами → `inputSchema` и `outputSchema`; `title`; `annotations`
  (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`);
  уровень доступа; список покрываемых методов `App`; обработчик. Ответ —
  `structuredContent` по `outputSchema` **плюс** короткое текстовое резюме в
  `content` (не дамп JSON). Аргументы — по модели идентификаторов MCP-01,
  старые имена — алиасы.
- Существующие 19 инструментов переводятся в реестр.
- **Тест паритета:** через `reflect` перебирает экспортированные методы
  `*App` и проверяет, что каждый либо покрыт инструментом реестра, либо в
  `excluded` (с непустой причиной), либо в `pending`. Заполнить `pending`
  по карте MCP-01. Тест падает также, если метод из `pending` уже покрыт
  (список должен сокращаться вместе с работой).
- В `CLAUDE.md`, матрица doc-sync, строка «New/changed Wails-bound method
  on `App`»: добавить «+ MCP-инструмент в реестре или запись в `excluded`
  (тест паритета)».

**Готово когда:** golden `tools/list` фиксирует контракт; тест проверяет
валидность каждого `structuredContent` по своей `outputSchema`; тест
паритета зелёный и ловит новый метод `App` без MCP (проверено временным
методом в тесте); рукописных JSON-схем строками не осталось.

---

## MCP-04: Стандарт описаний, ошибок и размера ответа

**Зависит от:** MCP-03
**Files:** реестр, `internal/control/rpc.go` (тексты ошибок), тест-линтер
реестра.

**Что сделать.** Установить стандарт, по которому делаются все
инструменты MCP-06…11, и привести к нему существующие:

- **Описание инструмента** по шаблону: что делает (1 фраза) → когда
  использовать и когда **не** (какой инструмент вместо) → что возвращает →
  побочные эффекты и уровень доступа → пример аргументов. Без внутреннего
  жаргона (`inputCh`, «атомики»).
- **Каждый параметр** — `description`, формат/пример, `enum` где значения
  конечны, значения по умолчанию в схеме.
- **Ошибки инструмента** (`isError: true`) — действенные: что не так и что
  делать дальше («сессия `x/y` не найдена; в проекте `x` есть A, B; список
  — `get_sessions`»; неверный статус → текущий статус и допустимые
  действия). Ошибки протокола — кодами JSON-RPC.
- **Размер ответа:** лимит по умолчанию, `cursor`/`next_cursor`, флаг
  `truncated` и подсказка в тексте.

**Готово когда:** тест-линтер падает при нарушении шаблона описания,
параметре без `description`, ответе больше лимита на фикстуре; раздел
«Ошибки» в `docs/mcp.md`.

---

## MCP-05: Уровни доступа и защита

**Зависит от:** MCP-04
**Files:** `cmd/cm-mcp/`, `internal/control/`, `config.example.toml` и
PLAN.md (если появится поле конфига).

**Что сделать.** До массового добавления инструментов:

- Режим `CM_MCP_MODE=read|operate|admin` (и/или поле конфига — по MCP-01),
  по умолчанию — решение MCP-01. Инструменты выше уровня не попадают в
  `tools/list`; вызов по имени даёт ошибку с подсказкой, как включить.
- Проверка уровня — на стороне **control-plane** тоже (токен с уровнем или
  заголовок режима), чтобы обход `cm-mcp` прямым HTTP не давал больше прав.
- Распределить существующие инструменты: `set_global_settings`,
  `stop_all`, `approve_permission`/`deny_permission` → `admin`.
- Защита от самоповреждения: сессия, запущенная менеджером, не может
  остановить/перезапустить/очистить **саму себя** без явного флага
  (идентификация — переменная окружения дочернего процесса; проверить,
  есть ли, иначе добавить).
- Журнал вызовов: каждое изменяющее действие (`operate`/`admin`) — в
  файловый лог менеджера: клиент (`clientInfo`), инструмент, аргументы без
  секретов, результат.
- Control-plane слушает только loopback; токен нигде не выводится —
  проверить тестами.

**Готово когда:** тесты на каждый режим (видимость в `tools/list`, отказ в
`cm-mcp` и в control-plane), самоостановку и журнал; раздел «Безопасность» в
`docs/mcp.md`; doc-sync по новому полю конфига, если есть.

---

## MCP-06: Область «Сессии» — полный жизненный цикл

**Зависит от:** MCP-05
**Files:** `internal/control/rpc.go`, реестр, тесты.

**Что сделать** (по шаблону доменной задачи). Покрыть: `StartSession`,
`StartSessionWithModel` / `StartSessionWithOverride` (модель, effort,
рантайм — параметрами `start_session`, а не отдельными инструментами),
`StartAdHocChatSession`, `StopSession` (мягко/жёстко), `RestartSession`,
`ResumeSession`, `StopAll`, `StartProject`, `StopProject`,
`SetSessionModel`, `SetSessionRuntime`, `ClearSessionState`,
`GetSessionState`, `GetSession`, `GetAllSessions`, `SendMessage`,
`SendMessageWithImages` (изображения — путь к файлу или base64 с лимитом
размера; решить и описать), `ListSlashCommands`, `GetRateLimitStatus`.
`get_session` возвращает всё, что нужно агенту для решения «что дальше»:
статус, модель, рантайм, текущая задача, последняя ошибка, ожидающие
вопросы/permissions/планы.

**Готово когда:** все перечисленные методы вне `pending`; тесты каждого
инструмента на `fakeclaude`; раздел «Сессии» в `docs/mcp.md`.

---

## MCP-07: Область «Взаимодействие» — вопросы, permissions, планы

**Зависит от:** MCP-06
**Files:** `internal/control/rpc.go`, реестр, тесты.

**Что сделать** (по шаблону). Покрыть: `GetPendingQuestions`,
`AnswerQuestion` (вопросы `AskUserQuestion`/`clarify`, RT-03),
`GetPendingPermissions`, `RespondPermission` (существующие
`approve_permission`/`deny_permission` — алиасы на единый
`respond_permission` или остаются, по MCP-01), `AddPermissionRule`,
`GetPermissionCandidates`, `GetPlan`, `ApprovePlan`. Одобрение
permissions, правил и планов — `admin` (MCP-05). Один инструмент-сводка
«что ждёт человека» (`get_pending` по всем видам, с фильтром по сессии)
— чтобы агенту не нужно было три вызова.

**Готово когда:** методы вне `pending`; e2e на `fakeclaude`: вопрос →
ответ через MCP → сессия продолжает; permission → решение через MCP;
раздел «Вопросы и permissions» в `docs/mcp.md`.

---

## MCP-08: Область «Проекты и конфигурация»

**Зависит от:** MCP-07
**Files:** `internal/control/rpc.go`, реестр, при необходимости
`internal/config` (точечные операции), тесты, `config.example.toml`/PLAN.md
(если меняется конфиг).

**Что сделать** (по шаблону).

- Чтение: `GetProjects`, `GetConfig` (эффективный конфиг **без
  секретов**), `HasClaudeMd`, `GetProjectMirror`.
- **Точечное изменение конфига** вместо «прислать весь `UpdateConfig`»:
  `add_project`, `update_project`, `remove_project`, `add_session`,
  `update_session`, `remove_session`, `update_global_settings` (белый
  список полей). Каждая операция валидирует вход тем же кодом, что GUI,
  возвращает diff «было/стало», поддерживает `dry_run: true`, проходит
  через тот же путь сохранения, что `UpdateConfig` (GUI обновляется
  событием). `update_config` целиком — только `admin`, с
  `If-Match`-подобной версией конфига против гонок с GUI.
- Действия над проектом: `InitGitRepo`, `GenerateClaudeMdSession`,
  `InstallSessionProtocol`, `MirrorProject`, `UnmirrorProject`,
  `SyncProjectMirrors`. Всё, что пишет в репозиторий пользователя, —
  `admin` (инвариант `LEARN-TASKS.md`).
- `PickDirectory` → `excluded` («путь передаётся аргументом `path`»);
  проверить, что путь существует, и вернуть понятную ошибку.

**Готово когда:** методы вне `pending`; тест «с пустого конфига через MCP:
добавить проект → добавить сессию → изменить её модель → `dry_run` не
меняет файл → удалить»; раздел «Проекты и конфигурация» в `docs/mcp.md`.

---

## MCP-09: Область «Логи и метрики»

**Зависит от:** MCP-08
**Files:** `internal/control/rpc.go`, реестр, тесты.

**Что сделать** (по шаблону).

- Логи: `GetSessionLog`, `GetHistory`, `GetProjectLogFiles`, `ExportLog`
  (возвращает путь к файлу, а не содержимое), `ImportProjectLogs`,
  `ClearProjectLogs` и `CleanOldLogs` (`admin`, разрушительные). Поиск по
  логу сессии с фильтром по типу события/тексту — если его нет в `App`,
  добавить в RPC, чтобы агент не выкачивал весь лог.
- Метрики: `GetSessionMetrics`, `GetDailyCost`, `GetProjectCost`,
  `GetDailyTokens`, `GetProjectTokens`, `GetTokenAttribution`,
  `GetTopActions`, `GetActionSamples`, `GetDurationProfile`,
  `GetModelRecommendation`, `GetAutoModelRouting`. Свести в небольшое
  число инструментов с параметром области (`get_cost`, `get_tokens`,
  `get_usage_profile`…), если это проще агенту.

**Готово когда:** методы вне `pending`; ответы в лимите размера на больших
фикстурах; раздел «Логи и метрики» в `docs/mcp.md`.

---

## MCP-10: Область «Preflight, roadmap, mixed programming»

**Зависит от:** MCP-09
**Files:** `internal/control/rpc.go`, реестр, тесты.

**Что сделать** (по шаблону). Покрыть: `RunPreflight`, `SessionPreflight`,
`ExecutePlan`, `GenerateRoadmap`, `ApproveRoadmap` (`admin`),
`GetLatestDraftRoadmap`, `GetSessionRoadmap`, `GetRoadmapRowDetail`,
`GetRoadmapTaskDetail`, `RegisterMixedBrief`, `DispatchMixedTask`,
`CancelMixedTask`, `GetMixedRounds`, `GetMixedQuality`. Долгие операции
здесь пока возвращают идентификатор и статус; progress и ожидание — в
MCP-12.

**Готово когда:** методы вне `pending`; тесты на `fakeclaude`/`fakeworker`;
раздел «Планирование и mixed» в `docs/mcp.md`.

---

## MCP-11: Область «Навыки» (experience layer)

**Зависит от:** MCP-10
**Files:** `internal/control/rpc.go`, реестр, тесты.

**Что сделать** (по шаблону). Покрыть: `GetSkills`, `GetSkillCandidates`,
`GetSkillQuality`, `GetSkillUsage`, `DistillSkill`, `ApproveSkill`
(`admin`), `ArchiveSkill`, `RestoreSkill`, `GetSkillAutopilot`,
`SetSkillAutopilot` (`admin`), `RunSkillAutopilot`. Учесть инварианты
`LEARN-TASKS.md` (всё новое выключено по умолчанию, промпт байт-в-байт при
выключенных флагах): MCP не включает функции в обход их флагов, а
сообщает агенту, какой флаг нужен.

**Готово когда:** методы вне `pending`; `pending` содержит только оконные
методы, ожидающие решения, или пуст; раздел «Навыки» в `docs/mcp.md`.

---

## MCP-12: Ожидание, progress, отмена и события

**Зависит от:** MCP-11
**Files:** `cmd/cm-mcp/`, `internal/control/wait.go`,
`internal/control/emit.go`, тесты.

**Что сделать.**

- Все долгие инструменты (`wait_for_status`, `wait_for_event`,
  `wait_for_worker_status`, preflight, roadmap, mixed, distill):
  таймаут по умолчанию и верхняя граница; `notifications/progress` при
  `progressToken`; `notifications/cancelled` реально прерывает ожидание на
  стороне control-plane; по таймауту — результат
  `{reached: false, current_status, …}` с подсказкой, а не ошибка.
- `wait_for_any`: ждать первого из событий «нужен человек» (вопрос,
  permission, план, ошибка, завершение задачи) по набору сессий — основной
  цикл агента-оператора без опроса.
- Если MCP-01 решил делать подписки — `notifications/resources/updated`
  для `cm://sessions/...` по событиям `emit.go`.
- Список типов событий `wait_for_event` — `enum` в схеме, из одного
  источника с `emit.go`.

**Готово когда:** тесты на progress, отмену (control-plane освобождает
ожидание), таймаут, `wait_for_any`; раздел «Ожидание и события» в
`docs/mcp.md`.

---

## MCP-13: Ресурсы и промпты

**Зависит от:** MCP-12
**Files:** `cmd/cm-mcp/` (resources, prompts), тесты, `docs/mcp.md`.

**Что сделать.**

- **Ресурсы:** `cm://guide` — руководство агента (встроено в бинарник,
  без второй копии `docs/mcp.md` — генерировать одно из другого);
  `cm://projects`, `cm://projects/{project}/config` (без секретов);
  `cm://sessions`, `cm://sessions/{project}/{session}`,
  `cm://sessions/{project}/{session}/log{?cursor}`. `mimeType`, `title`,
  `description` у каждого; `resources/list` с пагинацией.
- **Промпты** (готовые сценарии для пользователя клиента): «настроить
  проект и сессию с нуля», «запустить задачу и довести до конца»,
  «разобрать остановившуюся сессию», «разобрать всё, что ждёт человека»,
  «отчёт о расходах за период». Каждый ссылается на конкретные
  инструменты.
- `instructions` (MCP-02) ссылается на `cm://guide`.

**Готово когда:** тесты `resources/list`, `resources/read` по каждому
шаблону, `prompts/list`, `prompts/get`; тест, что ни ресурс, ни инструмент
не отдают токен и секреты; разделы «Ресурсы» и «Промпты» в `docs/mcp.md`.

---

## MCP-14: Соответствие спецификации и e2e полного управления

**Зависит от:** MCP-13
**Files:** `cmd/cm-mcp/*_test.go`, `internal/control/e2e_*`,
`testdata/scenarios/` (+ `scenarios_doc.md`), `scripts/`.

**Что сделать.**

- Conformance: клиент (из SDK или тестовый) поднимает `cm-mcp`
  подпроцессом по stdio и проходит каждый объявленный в capabilities
  метод; неподдерживаемые — `-32601`.
- e2e «полное управление»: реальный control-plane + `fakeclaude`, всё
  только через MCP — пустой конфиг → проект → сессия → запуск → вопрос и
  ответ → permission → лог и метрики → смена модели → перезапуск →
  остановка проекта → удаление сессии. Новый сценарий fakeclaude — с
  уникальным `match` (см. «Known gotchas» про `MatchScenario`) и записью в
  `scenarios_doc.md`.
- Opt-in скрипт проверки официальным MCP Inspector в CLI-режиме
  (`scripts/mcp-inspect.sh`; запускается вручную).

**Готово когда:** conformance и e2e в обычном `go test`; инструкция
Inspector в `docs/mcp.md` («Проверка»).

---

## MCP-15: Онбординг стороннего агента и doc-sync

**Зависит от:** MCP-14
**Files:** `docs/mcp.md`, `README.md`, `CLAUDE.md`,
`docs/testing-harness.md`, `.claude/skills/cm-task-finish/`, примеры
конфигов клиентов.

**Что сделать.**

- `docs/mcp.md` — полное руководство: быстрый старт (сборка `cm-mcp`,
  `control.json`), регистрация в клиентах (Claude Code `claude mcp add` /
  `.mcp.json`, Claude Desktop, Cursor, Codex — готовые фрагменты), режимы
  доступа и как их включить, модель предметной области (проект → сессия →
  задача → ход; статусы и переходы — диаграмма), рецепты (8–10 типовых
  задач с последовательностью вызовов), справочник инструментов по
  областям, ошибки, траблшутинг.
- Справочник инструментов и карта паритета **генерируются** из реестра
  (`go run ./cmd/cm-mcp -gen-docs` или `go generate`); тест падает при
  расхождении `docs/mcp.md` с реестром.
- `docs/testing-harness.md`: таблица «cm-mcp tools available to Claude» →
  ссылка на `docs/mcp.md`. `CLAUDE.md`: строка матрицы doc-sync «New
  `cm-mcp` tool» → `docs/mcp.md` (генерация); проверить
  `.claude/skills/cm-task-finish` на ссылки на старую таблицу.
- `README.md` — раздел «Управление из других ИИ (MCP)».

**Готово когда:** тест сверки документации зелёный; ссылки из
`CLAUDE.md`/`README.md`/`docs/testing-harness.md` актуальны; человек по
`docs/mcp.md` подключает `cm-mcp` к Claude Code с нуля.

---

## MCP-16: Оценка реальным агентом

**Зависит от:** MCP-15
**Files:** `testdata/mcp-eval/` (задачи и ожидаемые исходы), раннер
(`cmd/cm-mcp-eval/` или `scripts/`), `docs/mcp.md` («Оценка»).

**Что сделать.** Opt-in (`CM_MCP_EVAL=1`): тестовый экземпляр менеджера
на `fakeclaude` + `claude -p` с **только** `cm-mcp` в `--mcp-config`, без
доступа к репозиторию. 12–15 задач, покрывающих **все области** MCP-06…11,
с проверкой исхода по состоянию менеджера (не по тексту ответа):
настроить проект и сессию с нуля, запустить и дождаться `idle`, ответить
на вопрос, одобрить конкретный permission, найти сессию с ошибкой и
назвать причину, сменить модель и перезапустить, посчитать расходы
проекта, сгенерировать и показать roadmap, найти кандидатов в навыки,
мягко остановить проект и т.п. Метрики на задачу: успех, число вызовов,
ошибочных вызовов, токены. По результатам — правка описаний/ошибок и
повторный прогон.

**Готово когда:** раннер и задачи в репозитории; таблица «до/после
правок» в `docs/mcp.md`; успех ≥ 90% задач, ошибочных вызовов в среднем
< 1 на задачу (иначе задача остаётся открытой со списком причин).

---

## MCP-17: Закрыть блок

**Зависит от:** MCP-16
**Files:** `docs/mcp.md`, `docs/design-decisions.md`,
`docs/architecture.md`, `CLAUDE.md`, этот файл.

**Что сделать.** `pending` пуст — тест паритета это подтверждает; каждое
исключение в `excluded` имеет причину. Сверить `docs/mcp.md` с кодом;
в `docs/architecture.md` — `cm-mcp` и control-plane как публичный
интерфейс с полным паритетом; в `CLAUDE.md` строка `Developer 1` в таблице
ролей — MCP-01..17 done. Список deprecated-алиасов с планом удаления —
отдельной задачей в конец этого файла, если они остались.

**Готово когда:** `pending` пуст; агент, не видевший этот блок, по
`instructions` + `cm://guide` + `docs/mcp.md` полностью управляет
менеджером (подтверждено MCP-16); ссылки актуальны; гейт зелёный.

---

## Поправки по итогам MCP-01

Решения — в `docs/mcp.md`. Исполнители задач ниже читают это раньше текста самой задачи.

- **MCP-02.** Целевая ревизия — `2026-07-28` (current), а не `2025-11-25`. Нет `initialize`/`ping`/`logging/setLevel`; версия и capabilities — в `_meta` каждого запроса, есть обязательный `server/discover`. Сервер **dual-era**: legacy (`initialize`, `ping`) + modern. SDK — `github.com/modelcontextprotocol/go-sdk` `v1.8.0`+. Тест handshake — в обоих режимах; обязательно проверить, что SDK по stdio отвечает на оба вида запросов, и замерить старт `cm-mcp` (иначе запасной путь — только `2025-11-25`, см. «SDK» в `docs/mcp.md`).
- **MCP-03.** Инструменты `tools/list` — в детерминированном порядке (сортировка по имени). Реестр хранит для каждого метода `App`: имя инструмента, уровень, аннотации; исходные данные — таблица «Карта паритета» в `docs/mcp.md` (87 методов, 4 excluded). Тест паритета сверяет реестр с `grep` по экспортированным методам `*App` в `app*.go`.
- **MCP-12.** Вместо `notifications/resources/updated` — `subscriptions/listen` (modern) и `resources/subscribe` (legacy), только если SDK даёт их без собственного кода; иначе подписок нет, остаются `wait_for_*`.
- **MCP-13/14.** Capability `logging` не объявлять (deprecated, SEP-2577); диагностика — `stderr`.
- **MCP-05.** Уровни и правила отнесения уже зафиксированы в `docs/mcp.md` («Уровни доступа»); тестовая обвязка GUI запускается с `CM_MCP_LEVEL=admin`.
- **MCP-08.** Имена проектов и сессий не должны содержать `/`.
