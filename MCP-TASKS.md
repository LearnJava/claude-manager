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
| MCP-02 | ✓ DONE (2026-10-15) | cmd/cm-mcp/, go.mod |
| MCP-03 | ✓ DONE (2026-10-15) | internal/control/mcptools.go → реестр, тест паритета |
| MCP-04 | ✓ DONE (2026-10-15) | internal/control/mcptools_defs.go, mcperrors.go, mcplint_test.go, docs/mcp.md |
| MCP-05 | ✓ DONE (2026-10-15) | cmd/cm-mcp/, internal/control/access.go, internal/session/env.go, docs/mcp.md |
| MCP-06 | ✓ DONE (2026-10-15) | internal/control/rpc_sessions.go, mcptools_defs.go, docs/mcp.md |
| MCP-07 | ✓ DONE (2026-10-15) | internal/control/rpc_interaction.go, mcptools_interaction.go, docs/mcp.md |
| MCP-08 | ✓ DONE (2026-10-15) | internal/control/rpc_projects.go, cfgops.go, mcptools_projects.go, docs/mcp.md |
| MCP-09 | ✓ DONE (2026-10-15) | internal/control/rpc_logs.go, mcptools_logs.go, docs/mcp.md |
| MCP-10 | ✓ DONE (2026-10-15) | internal/control/rpc_planning.go, mcptools_planning.go, docs/mcp.md |
| MCP-11 | ✓ DONE (2026-10-15) | internal/control/rpc_skills.go, mcptools_skills.go, docs/mcp.md |
| MCP-12 | ✓ DONE (2026-10-15) | cmd/cm-mcp/server.go, internal/control/wait.go, emit.go, docs/mcp.md |
| MCP-13 | ✓ DONE | ресурсы и промпты: cmd/cm-mcp/ |
| MCP-14 | ✓ DONE (2026-10-15) | conformance и e2e: cmd/cm-mcp/*_test.go, testdata/scenarios/ |
| MCP-15 | ✓ DONE (2026-10-15) | docs/mcp.md, README.md, CLAUDE.md, docs/testing-harness.md |
| MCP-16 | ✓ DONE (2026-10-15) | cmd/cm-mcp-eval/, testdata/mcp-eval/, docs/mcp.md («Оценка реальным агентом») |
| MCP-17 | ✓ DONE (2026-10-15) | docs/mcp.md, docs/design-decisions.md, CLAUDE.md |
| MCP-18 | ✓ DONE (2026-10-15) | internal/control/mcptools_defs.go, docs/mcp.md |
| MCP-19 | ✓ DONE (2026-10-16) | internal/control/access.go, server.go, internal/session/env.go, frontend/tests/helpers/ |
| MCP-20 | ✓ DONE (2026-10-16) | internal/control/emit.go, wait.go, mcptools_defs.go |
| MCP-21 | ✓ DONE | cmd/cm-mcp/ (stdin.go, main.go, server.go), run-claude-manager.bat/.sh |
| MCP-22 | ✓ DONE (2026-10-16) | internal/control/mcptools_defs.go, rpc_interaction.go |
| MCP-23 | ○ TODO | internal/control/rpc_*.go, server.go, app_control.go |

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

---

## MCP-18: Удалить deprecated-алиасы

**Зависит от:** MCP-17
**Files:** `internal/control/mcptools_defs.go`, `cmd/cm-mcp/`, `docs/mcp.md`, этот файл.

**Что сделать.** Удалить алиасы, оставленные блоком для совместимости:
пара `project`+`session` вместо `session_id` (start/stop/interrupt/restart_session,
send_message, get_session_logs, wait_for_status), `tail` (вместо `limit`),
инструменты `get_metrics` (→ `get_cost`) и `set_global_settings`
(→ `update_global_settings`). Обновить тесты, `docs/mcp.md` (таблица
«Старые → новые аргументы»), `cm://guide`.

**Готово когда:** `grep -i deprecated` по реестру пуст; гейт зелёный.

---

## Поправки по итогам аудита 2026-10-16

Аудит готового блока: паритет полный (87 методов `App`, 7 исключений с
причинами), живой прогон `cm-mcp` по stdio прошёл (63 инструмента, 3
ресурса, 5 промптов). Найденные дефекты разложены на MCP-19…MCP-23.
По смыслу задачи независимы, но выполняются по порядку: они задевают
общие файлы (`mcptools_defs.go`, `docs/mcp.md`, golden `tools/list`).

---

## MCP-19: Уровни доступа без обхода и честная граница

**Зависит от:** —
**Files:** `internal/control/access.go`, `internal/control/server.go`,
`internal/control/e2e_runner.go`, `internal/session/env.go`,
`cmd/cm-mcp/main.go`, `frontend/tests/helpers/control.ts`,
`frontend/tests/helpers/bridge.ts`, `frontend/tests/global-setup.ts`,
`docs/mcp.md` («Уровни доступа», «Безопасность»), тесты
`internal/control/access_test.go`, `cmd/cm-mcp/access_test.go`.

**Проблема.**
1. `requestLevel` (`access.go:129`) считает запрос без заголовка
   `X-CM-Level` запросом уровня `admin`. Токен лежит в
   `~/.claude-manager/control.json`, и его читает любая сессия с Bash.
   Поэтому `curl /rpc` с токеном без заголовка получает `StopAll`,
   `ReplaceConfig` и одобрение permissions. Комментарий в `access.go:14`
   («cannot be bypassed by calling /rpc directly») не соответствует
   поведению.
2. Сессия-вызывающий определяется по заголовку `X-CM-Caller-Session`,
   который клиент пишет сам. Защита «не останавливай себя» обходится,
   если заголовок не отправить.
3. `selfTarget` (`access.go:144`) проверяет только Stop/Restart/Clear/
   StopAll/StopProject.

**Что сделать.**
1. Запрос без `X-CM-Level` → уровень `operate`, а не `admin`.
   Тестовая обвязка (`e2e_runner.go`, Playwright-хелперы
   `frontend/tests/helpers/*.ts`, `global-setup.ts`) явно отправляет
   `X-CM-Level: admin`. Найти всех прямых клиентов: `grep -rn "X-CM-Token"`
   без каталога `.claude/worktrees/`.
2. Токен сессии. При запуске каждой CLI-сессии менеджер выдаёт ей
   отдельный токен и кладёт его в окружение процесса
   (`CM_CONTROL_TOKEN`, рядом с `CM_SESSION_ID` в
   `internal/session/env.go`). Control-plane хранит отображение
   «токен → id сессии» и удаляет запись, когда сессия останавливается.
   Запрос с токеном сессии:
   - вызывающий берётся из этого отображения, заголовок
     `X-CM-Caller-Session` игнорируется;
   - уровень — как сейчас определяет `cm-mcp`. Отдельное ограничение
     уровня для сессий не вводить без решения пользователя.
   `cm-mcp` берёт `CM_CONTROL_TOKEN` из окружения раньше `control.json` —
   проверить и покрыть тестом.
3. Добавить в `selfTarget`: `RemoveSession`, `UpdateSession`,
   `SetSessionRuntime`, `ResumeSession`, `InterruptSession` (по id сессии)
   и `ClearProjectLogs` (по проекту). Точные имена RPC-методов и
   параметров взять из `rpcLevels`.
4. Исправить комментарий в `access.go` и разделы «Уровни доступа» и
   «Безопасность» в `docs/mcp.md`. Написать прямо: уровень — защита от
   ошибок агента, который работает только через MCP-инструменты. От агента
   с shell под тем же пользователем Windows он не защищает, потому что тот
   может прочитать `control.json`. Токен сессии закрывает подмену
   вызывающего и обход защиты от самоостановки.

**Готово когда:**
- Тест: запрос без `X-CM-Level` к `StopAll` получает отказ по уровню.
- Тест: запрос с токеном сессии A и заголовком
  `X-CM-Caller-Session: B` считается вызовом от A, `StopSession(A)`
  отклоняется защитой от самоостановки.
- Тест: каждый метод из п.3 по своей сессии отклоняется без
  `X-CM-Allow-Self`.
- Playwright-обвязка и e2e проходят. Гейт зелёный, `npm --prefix frontend
  test` зелёный.

---

## MCP-20: Ожидание без старых событий и все события GUI

**Зависит от:** —
**Files:** `internal/control/emit.go`, `internal/control/wait.go`,
`internal/control/mcptools_defs.go`, `internal/control/rpc_sessions.go`,
`internal/control/rpc_interaction.go`, `cmd/cm-mcp/server.go`,
`docs/mcp.md` («Ожидание и события»), тесты `internal/control/wait_test.go`.

**Проблема.**
1. `wait_for_status` и `wait_for_event` сначала ищут подходящее событие в
   кольцевом буфере (`wait.go:131-138`). Типичный сценарий
   `send_message` → `wait_for_status status=idle` сразу возвращает `idle`
   из времени до сообщения. `wait_for_any` уже игнорирует буфер
   (`TestWaitAny_IgnoresStaleRing`).
2. В `EventNames` (`emit.go:177`) нет пяти событий, которые слушает GUI:
   `alert:budget`, `alert:loop`, `skills:changed`, `skill:progress`,
   `experience:regression`. Enum схемы `wait_for_event` их отклоняет,
   поэтому агент не может среагировать на алерт по бюджету или
   зацикливанию.
3. `Subscribe` теряет события, когда заполнен буфер подписчика на 64
   слота. Всплеск `session:log` может вытеснить событие, которого ждёт
   waiter.

**Что сделать.**
1. Добавить в `EnvelopedEvent` поле `Seq uint64` — монотонный номер,
   который `ControlEmitter` присваивает при эмиссии.
2. Добавить в `waitRequest` поле `since_seq`. Из кольцевого буфера
   брать только события с `Seq > since_seq`. Если `since_seq` не задан:
   - `wait_for_status`: сначала текущий статус сессии через менеджер
     (`GetSession`). Совпал — вернуть сразу с `reached: true` и
     текущим `seq`. Иначе ждать новое событие, буфер не смотреть;
   - `wait_for_event`: прежнее поведение, но в описании инструмента
     написать, что без `since_seq` может вернуться прошлое событие.
3. `seq` в ответах. Ответ ожидания содержит `seq`. Инструменты действий,
   после которых обычно ждут (`send_message`, `start_session`,
   `restart_session`, `stop_session`, `interrupt_session`,
   `approve_permission`, `deny_permission`, `answer_question`), возвращают
   текущий `seq` эмиттера. В их `Returns` написать, что его передают в
   `since_seq`. Добавить параметр `since_seq` в схемы `wait_for_status` и
   `wait_for_event`.
4. Добавить пять событий в `EventNames` (константы — в пакетах, которые их
   эмитят). Тест: собрать имена из `EventsOn('…')` в `frontend/src`
   (регулярным выражением по файлам) и сравнить с `EventNames`. Каждое
   имя из фронтенда либо есть в списке, либо стоит в явном списке
   исключений с причиной (например, `app:ready`).
5. Подписка waiter'а фильтрует события по имени до того, как положить их
   в канал: в канал попадают только события, которые waiter может
   принять. Так `session:log` не заполняет буфер чужого ожидания.
6. `go run ./cmd/cm-mcp -gen-docs`, обновить golden `tools/list`.

**Готово когда:**
- Тест: после `idle` в буфере `send_message` → `wait_for_status idle` с
  `since_seq` из ответа `send_message` не возвращается раньше нового
  `idle`.
- Тест: `wait_for_status` без `since_seq` для уже простаивающей сессии
  возвращается сразу по текущему статусу.
- Тест: `wait_for_event event=alert:budget` принимается и срабатывает на
  эмиссию.
- Тест сверки с фронтендом проходит.
- Тест: 200 событий `session:log` подряд не мешают waiter'у
  `session:status` получить своё. Гейт зелёный.

---

## MCP-21: Устойчивый stdio и понятные ошибки

**Зависит от:** —
**Files:** `cmd/cm-mcp/server.go`, `cmd/cm-mcp/main.go`,
`cmd/cm-mcp/prompts.go`, `cmd/cm-mcp/resources.go`, новый
`cmd/cm-mcp/stdin.go`, `run-claude-manager.bat`, `run-claude-manager.sh`,
`docs/mcp.md` («Протокол», «Ошибки и траблшутинг», «Быстрый старт»),
тесты `cmd/cm-mcp/protocol_test.go`, `conformance_test.go`.

**Проблема.** Проверено живым прогоном `cm-mcp.exe` по stdio:
1. Одна плохая строка завершает процесс с кодом 1 без ответа. Плохая
   строка — это невалидный JSON (`{not json`), не-объект (`42`),
   сообщение без `"jsonrpc":"2.0"` или batch `[...]` после `initialize` с
   версией 2025-06-18+. Спецификация требует ответить ошибкой -32700 или
   -32600 и продолжить работу. Причина в SDK: `mcp/transport.go:524-549`,
   `:687`, `:705`.
2. Ответ control-plane 401 (`unauthorized`, обычный текст) показывается
   агенту как `decode rpc response: invalid character 'u'…`
   (`main.go:199-231`). HTTP-статус не проверяется.
3. Коды ошибок `0`: в `prompts.go:125` при пропущенном обязательном
   аргументе промпта (нужен -32602), в `resources.go:80-83` при
   недоступном control-plane (нужен -32603 с подсказкой «запустите
   Claude Manager»).
4. У обычных инструментов нет таймаута HTTP (`http.Client{}` в
   `main.go:118`). Если control-plane завис, `get_*` висит до отмены
   клиентом.
5. На закрытие stdin сразу после запроса ответ не пишется: `cm-mcp`
   выходит, не дождавшись обработчиков.
6. Лаунчеры не пересобирают `build/cm-mcp.exe`. После обновления
   приложения клиенты запускают старый `cm-mcp` с устаревшим списком
   инструментов.

**Что сделать.**
1. `stdin.go`: свой построчный читатель между `os.Stdin` и
   `mcp.IOTransport` (`server.go:425`). Он сам отвечает на плохие строки:
   невалидный JSON → `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,…}}`;
   не-объект, нет `jsonrpc:"2.0"`, `id: null` у запроса → -32600. Batch →
   -32600, если согласованная версия его не допускает. Можно проще:
   отвечать -32600 на любой batch, если клиенты 2025-03-26 от этого не
   ломаются; решение записать в `docs/mcp.md`. Ответы пишутся в stdout
   под общим мьютексом с транспортом, чтобы строки не перемешивались.
   Корректные объекты передаются дальше без изменений.
2. `main.go`: проверять `resp.StatusCode` до декодирования. 401/403 →
   «токен control-plane отклонён: приложение перезапущено и
   `control.json` перезаписан; перезапустите MCP-клиент или проверьте
   `CM_CONTROL_TOKEN`». Другие не-2xx → статус и первые 200 байт тела.
3. Коды ошибок из п.3 проблемы — через `jsonrpc.Error` с
   `CodeInvalidParams` / `CodeInternalError`.
4. Таймаут 60 с через контекст для инструментов, которые не long-running
   и не `wait_*`. Константа рядом с `LongToolTimeout`.
5. На EOF stdin дождаться выполняющихся обработчиков (не дольше 5 с) и
   только потом выйти.
6. Если SDK позволяет без форка: не отвечать на запрос, отменённый через
   `notifications/cancelled` (сейчас приходит `isError` «context
   canceled»). Если нельзя — описать в `docs/mcp.md` как известное
   отклонение.
7. `run-claude-manager.bat` / `.sh`: после сборки приложения собрать
   `go build -o build/cm-mcp.exe ./cmd/cm-mcp`. Ошибка сборки `cm-mcp` не
   блокирует запуск приложения, а печатается.

**Готово когда:**
- Тест: в один процесс подряд `initialize`, `{not json`, `42`,
  `{"id":1,"method":"ping"}` (без jsonrpc), batch, `ping`. На каждую плохую
  строку приходит ошибка с правильным кодом, последний `ping` получает
  ответ, процесс жив.
- Тест: control-plane-заглушка отвечает 401 → в `isError`-результате
  слово «token», а не «invalid character».
- Тест: `prompts/get` без обязательного аргумента → -32602.
- Тест: `initialize` и сразу закрытый stdin → ответ на `initialize`
  напечатан.
- Лаунчер собирает `build/cm-mcp.exe`. Гейт зелёный.

---

## MCP-22: Все решения по permissions и честные аннотации

**Зависит от:** —
**Files:** `internal/control/mcptools_defs.go`,
`internal/control/rpc_interaction.go`, `internal/control/access.go`
(`rpcLevels`), `internal/control/mcplint_test.go`, `docs/mcp.md`
(«Вопросы и permissions»), golden `tools/list`, тесты
`rpc_interaction_test.go`, `mcptools_test.go`.

**Проблема.**
1. В баннере GUI (`PermissionBanner.svelte`) есть решения `deny_always` и
   `allow_similar` (`internal/permission/handler.go:170,179`). В MCP их
   нет: `permissionScopes` (`mcptools_defs.go:243`) знает только once,
   session и always, а `deny_permission` всегда шлёт простой `deny`.
2. Аннотации непоследовательны. `stop_session` и `interrupt_session` не
   помечены `destructiveHint`, хотя `stop_project` с тем же смыслом
   помечен. У `approve_permission` и `deny_permission` все подсказки
   `false`, хотя одобрение разрешает сессии запустить произвольный
   инструмент.

**Что сделать.**
1. `approve_permission`: добавить `scope=similar` → `allow_similar`.
2. `deny_permission`: добавить параметр `scope` (`once` по умолчанию →
   `deny`, `always` → `deny_always`).
3. Уровни доступа. `always`-решения записываются в конфиг. Проверить,
   какой уровень сейчас требует `approve … always` и как это проверяется
   (уровень на инструмент или проверка аргумента в обработчике), и сделать
   так же для `deny … always`. `similar` живёт до конца сессии, поэтому
   требует тот же уровень, что `session`.
4. Аннотации: `stop_session` и `interrupt_session` → `destructiveHint:
   true`. `approve_permission` → `destructiveHint: true, openWorldHint:
   true`. Read-only инструменты → `idempotentHint: true`, если это одна
   правка в общем конструкторе. Дополнить `mcplint_test.go` правилом:
   инструмент, который останавливает или прерывает сессию, помечен
   destructive.
5. `go run ./cmd/cm-mcp -gen-docs`, обновить golden `tools/list`.

**Готово когда:**
- Тест: `deny_permission scope=always` доходит до обработчика как
  `deny_always` и требует того же уровня, что `approve … always`.
- Тест: `approve_permission scope=similar` доходит как `allow_similar`.
- `tools/list` показывает новые аннотации. `TestDocsInSync` и гейт
  зелёные.

---

## MCP-23: RPC вызывает `App`, а не менеджер в обход

**Зависит от:** —
**Files:** `internal/control/server.go` (интерфейсы для `*App`),
`internal/control/rpc_sessions.go`, `rpc_interaction.go`,
`rpc_projects.go`, `rpc_logs.go`, `rpc_planning.go`, `rpc_skills.go`,
`app_control.go`, тесты `internal/control/*_test.go`, `docs/mcp.md`
(«Реестр инструментов и тест паритета»).

**Проблема.** Тест паритета сверяет только имена: у инструмента в
`Covers` указан метод `App`. Многие RPC-обработчики при этом вызывают
`SessionManager` напрямую (`server.go:26-63`, обработчики в `rpc_*.go`),
мимо `App`. Логика, добавленная позже в обёртку `App.X`, в MCP тихо не
попадёт. Так уже было со `StartProject`: `App` добавляет порядок запуска
по cache-affinity, а `rpc_sessions.go:131` повторяет его только при
наличии app. Это нарушает инвариант блока «GUI и MCP — один код».

**Что сделать.**
1. Аудит: таблица «RPC-метод → что вызывает (`App.X` или `Manager.Y`) →
   есть ли у `App.X` логика сверх вызова менеджера». Положить её в
   описание коммита, не в репозиторий.
2. Для каждого RPC, где `App.X` делает больше, чем `Manager.Y`, вызывать
   `App.X` через узкий интерфейс области (`SessionApp`, `ProjectsApp`,
   `InteractionApp`… по образцу существующих в `server.go`). Добавить
   проверки `var _ control.XxxApp = (*App)(nil)` в `app_control.go`.
   Тесты `internal/control` получают фейковую реализацию интерфейса.
3. Где `App.X` — чистая обёртка над `Manager.Y`, допустимо оставить
   прямой вызов. Записать правило в `docs/mcp.md`: «RPC вызывает `App`,
   если у метода `App` есть своя логика; новая логика добавляется в
   `App`, а не в RPC».
4. Если правок больше ~400 строк, сделать в этой задаче область «Сессии»
   (в ней найдена расходимость), а остальные области дописать в конец
   этого файла задачами MCP-24… и поставить их указатели в `STATUS-P1.md`
   сразу после указателя этой задачи.

**Готово когда:**
- Каждый RPC с логикой в `App.X` вызывает `App.X`. `StartProject` через
  RPC запускает сессии в том же порядке, что и кнопка GUI (тест с фейком,
  который записывает вызовы).
- Проверки `var _` компилируются. Гейт зелёный.
