# Claude Manager как MCP-сервер

Руководство и решения блока `MCP-TASKS.md`. Файл создан в MCP-01 и растёт
с каждой задачей блока: MCP-02 добавляет «Протокол», MCP-06…11 — разделы
областей, MCP-15 — онбординг и генерируемые таблицы. Раздел «Карта паритета» — источник, из
которого MCP-03 строит реестр инструментов и тест паритета.

Данные на 2026-10-15: по коду (`файл:функция`) и по modelcontextprotocol.io.

## Спецификация

**Целевая ревизия — `2026-07-28`** (на modelcontextprotocol.io помечена
*current*; предыдущая — `2025-11-25`). План блока писался под `2025-11-25`;
новая ревизия **меняет жизненный цикл**, поэтому ниже — что из неё следует.

### Что изменилось в `2026-07-28` (страница Key Changes)

| Было (`2025-11-25`) | Стало (`2026-07-28`) | Что делаем |
|---|---|---|
| `initialize` / `notifications/initialized`, сессия | Нет рукопожатия: версия, `clientCapabilities`, `clientInfo` в `_meta` **каждого** запроса; `UnsupportedProtocolVersionError` (-32022) со списком `supported` | Сервер **dual-era**: отвечает и на `initialize` (старые клиенты), и на запросы с `_meta` |
| — | Обязательный RPC `server/discover` (версии, capabilities, identity) | Отдаёт SDK; `instructions` задаём сами (MCP-02) |
| `ping`, `logging/setLevel` | Удалены; уровень лога — `_meta["io.modelcontextprotocol/logLevel"]` | `ping` — только в legacy-режиме SDK; логи — `stderr` (Logging deprecated, SEP-2577) |
| `resources/subscribe`, GET-поток | `subscriptions/listen` (`toolsListChanged`, `promptsListChanged`, `resourcesListChanged`, `resourceSubscriptions`) | MCP-12: подписки на `cm://sessions/…`, если SDK отдаёт их без своего кода |
| server→client запросы (sampling, elicitation, roots) | Multi Round-Trip Requests (`resultType: "input_required"`) | Не используем (см. «Сознательно не делаем») |
| Tasks в ядре | Расширение `io.modelcontextprotocol/tasks` | Не используем: долгие операции — `wait_for_*` + progress (MCP-12) |
| — | Результаты `*/list`, `resources/read` несут `ttlMs`, `cacheScope`; `tools/list` в детерминированном порядке | Порядок — сортировка по имени в реестре (MCP-03); `ttlMs` ставит SDK |
| Узкий JSON Schema для `inputSchema`/`outputSchema` | Любые ключевые слова JSON Schema 2020-12; `structuredContent` — любой JSON | Схемы выводим из Go-структур (MCP-03) |
| Resource not found `-32002` | `-32602` | Отдаёт SDK |
| — | Коды -32020…-32099 зарезервированы за MCP; -32000…-32019 — реализация | Свои коды не вводим: ошибки инструмента — `isError: true` |

Поправки к задачам блока, которые из этого следуют (внесены в
`MCP-TASKS.md`): **MCP-02** — dual-era вместо «`ping`/`initialized`»,
тест в обоих режимах; **MCP-12** — `subscriptions/listen` (modern) и
`resources/subscribe` (legacy) вместо `notifications/resources/updated`;
**MCP-13/14** — capability `logging` не объявляем.

### Что сервер обязан поддержать

| Возможность | Решение | Основание |
|---|---|---|
| Версии `2026-07-28` и `2025-11-25` (и старше, что даёт SDK) | **да** | клиенты в поле ещё на `2025-11-25` |
| `server/discover` + `UnsupportedProtocolVersionError` | **да** | обязателен в `2026-07-28` |
| `tools`: `title`, `description`, `inputSchema`, `outputSchema`, `structuredContent`, `annotations` (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) | **да** | значения — в карте ниже |
| Разделение ошибок: протокол (JSON-RPC `error`) и инструмент (`isError: true` + подсказка) | **да** | MCP-04 |
| Пагинация (`cursor` / `nextCursor`) в `*/list` и в наших больших ответах | **да** | инвариант «вывод экономен» |
| `resources` (+ `resources/templates/list`), `resources/read` | **да** | MCP-13 |
| `prompts` | **да** | MCP-13 |
| Отмена `notifications/cancelled`, progress `notifications/progress` | **да** | MCP-12 |
| Подписки (`subscriptions/listen`, `resources/subscribe`) | **по возможности** | MCP-12 |
| `completion/complete` | нет | аргументы — идентификаторы из `get_sessions`/`list_projects`; не окупается |

### Сознательно не делаем

- **Streamable HTTP.** Control-plane локальный, токен в
  `~/.claude-manager/control.json`; `cm-mcp` — stdio-подпроцесс клиента.
  HTTP-сервер добавил бы сетевую поверхность и авторизацию (OAuth) без
  выигрыша: все целевые клиенты (Claude Code/Desktop, Cursor, Codex, Hermes)
  умеют stdio. Пересмотреть, если появится клиент без stdio.
- **Sampling, roots, elicitation, MRTR** — сервер ничего не просит у клиента;
  то, что нужно человеку (вопрос сессии, permission), — состояние менеджера,
  доступное через инструменты. Sampling и roots к тому же deprecated.
- **Logging (capability)** — deprecated; диагностика в `stderr`.
- **Tasks-расширение** — достаточно `wait_for_*` с progress и отменой.
- **OAuth-авторизация MCP** — для stdio не нужна; защита — уровни доступа.

## SDK

**Решение: официальный `github.com/modelcontextprotocol/go-sdk`, `v1.8.0`
(2026-09-04) и новее; самописный `cmd/cm-mcp/main.go` не дорабатываем.**
Версия в `go.mod` фиксируется, обновляется осознанно.

| Критерий | Проверка | Результат |
|---|---|---|
| Спецификация | таблица совместимости в README SDK | `v1.7.0+`: latest `2026-07-28`; также `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05` |
| Без CGO | поиск `import "C"`/`cgo` в исходниках `v1.8.0` | нет; зависимости на чистом Go (`google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate`, `x/oauth2`, `golang-jwt`, `x/time`) |
| Go | `go.mod` SDK: `go 1.25.0`; у нас `go 1.25.0`, тулчейн 1.27.1 | совместимо |
| Схемы из структур | `mcp.AddTool[In, Out]` (`mcp/server.go`) выводит и проверяет `inputSchema`/`outputSchema` | да |
| Возможности | `NewServer`, `Server.Run(ctx, &StdioTransport{})`, `AddPrompt`, ресурсы и шаблоны, `ServerSession.NotifyProgress`, `ServerOptions.PageSize` (по умолчанию 1000), `server/discover`, `subscriptions/listen`, `ttlMs`/`cacheScope` | есть (`mcp/server.go`, `mcp/protocol.go`, `mcp/transport.go`) |
| Зрелость | 33 релиза с `v0.1.0`; `2026-07-28` поддержан в `v1.7.0` | активно развивается; риск — частые изменения API |

Что MCP-02 обязан закрыть тестом, а не предположением:

1. Сервер SDK отвечает **и** на `initialize`, **и** на запрос с `_meta` без
   рукопожатия по stdio (dual-era).
2. Размер бинарника и время старта `cm-mcp` (клиент запускает его на каждую
   сессию) — замерить до и после.
3. Поведение существующих инструментов (`cmd/cm-mcp/main_test.go`) не меняется.

Если п.1 не выполняется, запасной путь — писать только `2025-11-25` (SDK это
умеет) и вернуться к `2026-07-28` обновлением SDK.

## Протокол

Реализован в MCP-02. `cmd/cm-mcp/server.go` — тонкий слой над
`github.com/modelcontextprotocol/go-sdk` `v1.8.0`; транспорт — stdio
(`mcp.IOTransport`), набор и поведение инструментов не менялись
(`internal/control/mcptools.go`, `main.go:callToolCtx`).

- **Dual-era.** Один сервер отвечает и на `initialize` (клиент просит
  `2025-11-25`/`2024-11-05` — получает её же; неизвестную версию — получает
  поддерживаемую), и на запросы с `_meta` без рукопожатия плюс
  `server/discover` (`2026-07-28`). Проверено тестами
  `cmd/cm-mcp/protocol_test.go` по сырому JSON-RPC.
- `serverInfo`: `name=cm-mcp`, `title=Claude Manager`, `version` из
  `-ldflags "-X main.version=…"` (по умолчанию `dev`). В modern-режиме
  идентичность лежит в `_meta["io.modelcontextprotocol/serverInfo"]`.
- `instructions` — 8 строк: что такое менеджер, формат `project/session`,
  жизненный цикл статусов, «не опрашивай — жди через `wait_for_*`»,
  экономия вывода, как читать ошибку «control-plane unreachable», ссылка на
  этот файл (ресурс `cm://guide`).
- `capabilities`: `tools`, `resources`, `prompts` (без `listChanged`: наборы фиксированы) —
  capability `logging` намеренно не объявляется.
- `ping` и `notifications/cancelled` обрабатывает SDK; отмена доходит до
  обработчика через `ctx` и обрывает HTTP-вызов в control-plane (в том числе
  долгий `wait_for_*`).
- stdout — только протокол; логгер SDK пишет в stderr (уровень warn).
- Недоступный control-plane — результат инструмента `isError: true` с
  подсказкой «start Claude Manager and retry …», сервер продолжает работать.

Замер (Windows, go1.27.1): бинарник 16,4 → 19,1 МБ; запуск процесса с
`initialize` ~270 мс как до, так и после (доминирует старт процесса).

## Идентификаторы

**Один способ адресовать сессию: `session_id = "project/session"`**
(формат менеджера, `CLAUDE.md` → Conventions). Проект — `project`. Имена
проектов и сессий не содержат `/` (проверяет MCP-08 при создании).

| Инструмент | Аргументы адресации |
|---|---|
| `start_session`, `stop_session`, `interrupt_session`, `restart_session` | `session_id` |
| `send_message` | `session_id`, `text` |
| `get_session_logs` | `session_id`, `limit`, `cursor` |
| `wait_for_status` | `session_id`, `status`, `timeout_ms` |
| `approve_permission`, `deny_permission` | `session_id`, `request_id`, `scope` |
| `run_preflight`, `get_mixed_rounds`, `dispatch_mixed_task` | `project` (проектные) |
| `wait_for_event` | `event`, `match` (`match` может содержать `session_id`) |

Прежние алиасы удалены в MCP-18: пара `project`+`session` вместо `session_id`,
`tail` вместо `limit`, `get_metrics` (стоимость — `get_cost`, rate limit —
`get_rate_limit`), `set_global_settings` (вместо него `update_global_settings`).
Неизвестный аргумент сервер отвергает по схеме.

## Уровни доступа

Три уровня, задаются при запуске `cm-mcp` (флаг `--level` или переменная
`CM_MCP_LEVEL`); реализация — `internal/control/access.go`, `cmd/cm-mcp`. Инструменты выше уровня **не попадают
в `tools/list`**, а вызов по имени возвращает ошибку инструмента
«требуется уровень admin».

| Уровень | Что разрешает | Методов `App` в карте |
|---|---|---|
| `read` | только чтение: состояние, логи, метрики, справка | 40 |
| `operate` (**по умолчанию**) | обычная работа оператора: запуск/остановка отдельной сессии, сообщения, ответы на вопросы, отказ в permission, preflight/roadmap-черновики, дистилляция черновиков | 24 |
| `admin` | одобрение вместо человека, смена конфигурации, запись в репозиторий пользователя, удаление данных, массовая остановка | 19 (ещё 4 метода `excluded`) |

Правила отнесения (действуют и для новых методов; тест MCP-03 их проверяет):

- пишет в репозиторий пользователя (`approve_skill`, `approve_roadmap`,
  `install_session_protocol`, `init_git_repo`, `generate_claude_md`) →
  `admin` (инвариант `LEARN-TASKS.md`);
- одобряет за человека (`approve_permission`, `approve_plan`,
  `add_permission_rule`) → `admin`; **отказ** (`deny_permission`) — `operate`;
- меняет конфигурацию (`update_*`, `add_*`/`remove_*`, `set_skill_autopilot`,
  mirror) → `admin`;
- удаляет данные (`clear_*`, `clean_old_logs`, `remove_*`) → `admin` и
  обязательный `confirm=true` (иначе ошибка с объяснением);
- массовая остановка (`stop_all_sessions`, `stop_project`) → `admin`;
  остановка одной сессии — `operate` (обратимо: `start_session`/resume);
- запуск модели за деньги (preflight, roadmap, distill) → `operate` — рамка
  стоимости задаётся `max_budget_usd` проекта.

**Чем защищён `admin`:** не включается по умолчанию и не может быть
включён самим агентом (инструмента повышения уровня нет); уровень берётся из
окружения процесса `cm-mcp`, которое задаёт человек в конфиге клиента.
Control-plane проверяет уровень и на своей стороне (запрос `cm-mcp`
несёт его в заголовке `X-CM-Level`; без заголовка — `operate`). Это защита от
ошибок, а не от агента с shell — см. «Безопасность». Тестовая обвязка GUI (`docs/testing-harness.md`) запускает `cm-mcp` с
`CM_MCP_LEVEL=admin` — так остаются рабочими существующие сценарии
(`approve_permission` в Playwright-тестах).

## Безопасность

- **Уровень задаёт человек.** `--level` или `CM_MCP_LEVEL` в конфиге MCP-клиента;
  по умолчанию `operate`; неизвестное значение — `cm-mcp` не стартует. Инструмента
  повышения уровня нет.
- **Два слоя.** `cm-mcp` не публикует в `tools/list` инструменты выше уровня, а
  вызов по имени возвращает ошибку инструмента `requires access level admin: …
  CM_MCP_LEVEL=admin`. Control-plane проверяет каждый RPC-метод по таблице
  `rpcLevels` (`internal/control/access.go`; метод без записи требует `admin`;
  `RespondPermission` с `deny` — `operate`, с любым другим решением — `admin`).
  Ошибки control-plane: `-32001` (уровень), `-32003` (самоповреждение).
- **Граница.** Уровень — защита от ошибок агента, который работает только через
  MCP-инструменты. От агента с shell под тем же пользователем Windows он **не**
  защищает: тот читает `~/.claude-manager/control.json` и сам шлёт
  `X-CM-Level: admin`. `cm-mcp` всегда шлёт `X-CM-Level`; запрос **без** заголовка
  считается `operate` (раньше — `admin`), а тестовая обвязка и e2e-раннер шлют
  `X-CM-Level: admin` явно.
- **Токен сессии.** Менеджер выдаёт каждой CLI-сессии свой токен и кладёт его в
  окружение как `CM_CONTROL_TOKEN` (рядом с `CM_SESSION_ID`); `cm-mcp` берёт его
  из окружения раньше `control.json`. Control-plane сопоставляет токен с id сессии
  и удаляет запись, когда процесс сессии завершился. Для запроса с токеном сессии
  вызывающий — эта сессия, заголовок `X-CM-Caller-Session` игнорируется. Так
  нельзя ни подменить вызывающего, ни обойти защиту от самоостановки, не отправив
  заголовок. Уровень токен не ограничивает.
- **Самоповреждение.** Менеджер кладёт в окружение каждой сессии
  `CM_SESSION_ID=project/session`. Отклоняются, пока человек не задал
  `CM_MCP_ALLOW_SELF=1` (или `--allow-self`): `StopSession`, `RestartSession`,
  `InterruptSession`, `ResumeSession`, `SetSessionRuntime` (по `id` своей
  сессии), `RemoveSession`, `UpdateSession`, `ClearSessionState` (по
  project+session своей сессии), `StopProject`, `ClearProjectLogs` (по проекту
  своей сессии) и `StopAll`.
- **Журнал.** Каждый вызов не-`read` метода (в т.ч. отказ) — запись
  `control.audit` в `app.log` менеджера: `client` (MCP `clientInfo`), `level`,
  `method`, `params`, `result` (`ok` / `error` / `denied:level` / `denied:self`).
  В `params` значения полей с `token|secret|key|password|authorization|credential`
  в имени заменены на `***`, строки обрезаны до 200 символов.
- **Сеть.** Control-plane слушает только `127.0.0.1`; токен не пишется в лог
  (тест `TestTokenNeverLogged`).

## Действия GUI мимо методов `App`

Проверено по `frontend/src` (поиск `EventsEmit`, `localStorage`,
импортов `wailsjs/runtime` и `wailsjs/go`):

- Frontend вызывает Go **только** через `wailsjs/go/main/App` (24 файла
  импортируют этот модуль) и `wailsjs/go/models` (типы). Прямых
  `EventsEmit` из UI в Go нет.
- `wailsjs/runtime` используется только для `EventsOn` — подписки на
  события бэкенда (`stores/sessions.ts`, `stores/workers.ts`,
  `ExperiencePanel.svelte`, `Settings.svelte`, `SkillReview.svelte`); это
  канал «Go → UI», его аналог для агента — `wait_for_event` и ресурсы.
- Состояние, живущее только во фронтенде (`localStorage`): `cm.locale`
  (`lib/i18n.ts`), `cm.theme` (`stores/theme.ts`), `cm.costUnit`
  (`stores/units.ts`), `cm.logMarkdown`, `cm.logLayout`
  (`stores/logView.ts`), `cm.sidebarWidth` (`App.svelte`). **Решение:** все
  шесть — чисто визуальные настройки одного окна; бэкенд их не использует
  и результатов работы не меняют → **исключение**, переносить на бэкенд не
  нужно.
- Нативные окна/диалоги (`PickDirectory`, `ShowMainWindow`,
  `MinimizeToTray`, `Notify`) — методы `App`, в карте помечены `excluded`.
- Трей-меню (`app.go`, systray: Show/Quit) — вне `App`; «Quit» завершает
  менеджер вместе со всеми сессиями (`CLAUDE.md` → Known gotchas) и
  **сознательно недоступно** агенту: ни один инструмент не убивает и не
  перезапускает `claude-manager.exe`.

Новых недостающих действий GUI, требующих переноса на бэкенд, нет.

## Быстрый старт

1. Собрать `cm-mcp` (рядом с приложением) и запустить менеджер:

   ```bash
   go build -o build/cm-mcp.exe ./cmd/cm-mcp
   ```

   Запущенное приложение пишет адрес и токен control-plane в
   `~/.claude-manager/control.json`; `cm-mcp` читает его сам. Переопределения:
   `CM_CONTROL_ADDR`, `CM_CONTROL_TOKEN`. Приложение не запущено — `tools/list`
   работает, `tools/call` вернёт «control-plane unreachable».
2. Зарегистрировать сервер в клиенте (ниже). Уровень по умолчанию — `operate`;
   `admin` включается только в конфиге клиента (`--level admin` или
   `CM_MCP_LEVEL=admin`), см. «Уровни доступа».
3. Проверить: попросить агента вызвать `get_sessions` — вернётся список сессий.

### Регистрация в клиентах

Путь к `cm-mcp.exe` — абсолютный. Во всех примерах уровень `operate`; для
другого добавьте `"--level", "admin"` в `args` (или `-e CM_MCP_LEVEL=admin`).

**Claude Code**

```bash
claude mcp add cm -- D:/GoProjects/claude-manager/build/cm-mcp.exe
claude mcp add cm -e CM_MCP_LEVEL=admin -- D:/GoProjects/claude-manager/build/cm-mcp.exe   # admin
```

или `.mcp.json` в корне проекта:

```json
{
  "mcpServers": {
    "cm": { "command": "D:/GoProjects/claude-manager/build/cm-mcp.exe", "args": [] }
  }
}
```

**Claude Desktop** (`%APPDATA%\Claude\claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "cm": { "command": "D:\GoProjects\claude-manager\build\cm-mcp.exe", "args": ["--level", "operate"] }
  }
}
```

**Cursor** (`~/.cursor/mcp.json` или `.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "cm": { "command": "D:/GoProjects/claude-manager/build/cm-mcp.exe", "args": [], "env": { "CM_MCP_LEVEL": "operate" } }
  }
}
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.cm]
command = "D:/GoProjects/claude-manager/build/cm-mcp.exe"
args = ["--level", "operate"]
```

Транспорт — только stdio. Подробности протокола — «Протокол»; проверка
соединения без клиента — «MCP Inspector».

## Модель предметной области

**Проект** (папка + `config.toml`) → **сессия** (`{project}/{session}`,
`session_id`) → **задача** (строка очереди `*-STATUS.md`, `task_id`) → **ход**
(одно сообщение пользователя и ответ модели). Агент оперирует `session_id`
из ответов, не конструируя идентификаторы.

Статусы сессии и переходы:

```
idle ──start_session──▶ starting ──▶ working ◀──send_message──┐
 ▲                                     │  │                    │
 │                                     │  └─ вопрос ─▶ waiting_for_user ──answer_question─┘
 │                                     └──── permission ─▶ waiting_permission ──approve/deny─┘
 └── stop_session / завершение ◀───────┘   (ошибка → error; лимит → rate_limited → retrying)
```

Полный перечень статусов — enum аргумента `wait_for_status` (есть ещё `analyzing`, `stopping`); ждать
перехода — `wait_for_status` / `wait_for_any`, а не опрашивать в цикле.

## Рецепты

| Задача | Вызовы по порядку |
|---|---|
| Обзор | `get_sessions` → `get_session` по интересующей |
| Подключить проект | `add_project` (`dry_run` сначала) → `add_session` → `start_session` |
| Дать поручение и дождаться | `send_message` → `wait_for_status` (`waiting_*`/`idle`) → `get_session_logs` |
| Разобрать ожидающее человека | `get_pending` → `approve_permission` / `deny_permission` / `answer_question` |
| Следить за несколькими | `wait_for_any` по списку сессий → действовать по причине пробуждения |
| Сменить модель | `stop_session` → `set_session_model` → `start_session` (или `restart_session`) |
| Расход | `get_cost` (project, day) / `get_session_metrics`; лимиты аккаунта — `get_rate_limit` |
| Найти в логе | `search_session_log` (level/tool/text) → `export_log` |
| Спланировать перед запуском | `session_preflight` → `get_plan` → `approve_plan` → `execute_plan` |
| Остановить всё | `stop_project` / `stop_all_sessions` (`confirm=true`, admin) |

Готовые сценарии с именованными аргументами — «Промпты».

## Справочник инструментов

Генерируется из реестра; описание — первая фраза `description`
(полный формат — «Стандарт описаний»). Признаки — аннотации MCP.

<!-- BEGIN GENERATED: tools -->

| Инструмент | Уровень | Признаки | Что делает |
|---|---|---|---|
| `start_session` | operate | идемпотентное, запускает модель | Start a configured session, optionally with a model, effort or runtime override. |
| `stop_session` | operate | идемпотентное | Stop a running session. |
| `interrupt_session` | operate | идемпотентное | Interrupt the session's current turn (a running tool call or generation) without stopping the session. |
| `restart_session` | operate | запускает модель | Restart a session, fresh or resuming its saved conversation. |
| `send_message` | operate | запускает модель | Send a user message, optionally with images, to a running session. |
| `set_session_model` | operate | идемпотентное | Switch a session's model, live if it is running. |
| `set_session_runtime` | operate | идемпотентное | Choose which agent CLI (claude or hermes) a session drives. |
| `clear_session_state` | admin | разрушительное, идемпотентное | Delete a session's saved crash-recovery state so its next start opens a fresh conversation. |
| `start_project` | operate | идемпотентное, запускает модель | Start every configured session of a project. |
| `stop_project` | admin | разрушительное, идемпотентное | Stop every running session of a project. |
| `stop_all_sessions` | admin | разрушительное, идемпотентное | Stop every running session of every project. |
| `start_ad_hoc_chat` | operate | идемпотентное, запускает модель | Open an unconfigured Chat session in a project, with no task source and no first prompt. |
| `approve_permission` | admin | — | Approve a pending permission request on the human's behalf. |
| `deny_permission` | operate | — | Deny a pending permission request. |
| `run_preflight` | operate | запускает модель | Analyse a task in a project and produce an execution plan. |
| `execute_plan` | operate | запускает модель | Start executing a plan produced by run_preflight. |
| `register_mixed_brief` | operate | идемпотентное | Register a self-contained brief (English task text, verbatim code excerpts, decisions, target files) for a worker model. |
| `dispatch_mixed_task` | operate | запускает модель | Run the brief, patches, checks, feedback loop for a registered brief against a worker model. |
| `get_mixed_rounds` | read | только чтение | List the saved mixed-programming tasks of a project with rounds, patches, check results and status. |
| `wait_for_worker_status` | read | только чтение | Block until a mixed-programming task reaches a terminal status, or the timeout passes. |
| `get_sessions` | read | только чтение | List all sessions with their status and metrics. |
| `get_session` | read | только чтение | Get one session's full state: status, model, runtime, current task, pending questions and permissions, saved conversation state. |
| `list_slash_commands` | read | только чтение | List the slash commands and skills a session offers after a leading /. |
| `get_session_logs` | read | только чтение | Read a session's log of its latest run, oldest entry first, one page at a time. |
| `get_pending_permissions` | read | только чтение | List permission requests that wait for a decision, across all sessions. |
| `get_rate_limit` | read | только чтение | Get the account rate-limit status. |
| `wait_for_status` | read | только чтение | Block until a session reports the given status, or the timeout passes. |
| `wait_for_event` | read | только чтение | Block until an event whose payload contains the given fields is emitted, or the timeout passes. |
| `wait_for_any` | read | только чтение | Block until the first of several sessions needs a human (question, permission, plan, error, finished task or turn), or the timeout passes. |
| `get_pending` | read | только чтение | List every permission request and question that waits for a human, across all sessions or for one. |
| `get_pending_questions` | read | только чтение | List the questions sessions asked the human (AskUserQuestion, clarify, the manager's own restart choice) that still wait for an answer. |
| `answer_question` | operate | запускает модель | Answer a question a session asked the human. |
| `add_permission_rule` | admin | идемпотентное | Add a standing allow or deny rule to one session's permission rules. |
| `get_permission_candidates` | read | только чтение | Suggest auto-allow rules from a project's permission history: requests that keep recurring and were always approved. |
| `get_plan` | read | только чтение | Get a persisted plan with its subtasks and their progress. |
| `approve_plan` | admin | идемпотентное | Persist a plan, optionally edited, as approved on the human's behalf. |
| `get_projects` | read | только чтение | List the configured projects with their folder, session names and mirror and mixed-programming flags. |
| `get_config` | read | только чтение | Return the effective application config (global settings, optimization, projects with sessions, workers) in config.toml shape, without secrets, with its version. |
| `check_environment` | read | только чтение, запускает модель | Find the programs sessions rely on (Claude CLI, Git, Git Bash, Python, build tools) and the install command for each missing one. |
| `get_project_setup` | read | только чтение | Report whether a project's folder has a CLAUDE.md and the developer-session protocol, and its mirror state. |
| `add_project` | admin | идемпотентное | Register a project folder under a new name. dry_run:true returns the diff and saves nothing. |
| `update_project` | admin | идемпотентное | Change fields of a project: default permission mode, mixed programming, gates, journal, skills and mirror flags. dry_run:true returns the diff and saves nothing. |
| `remove_project` | admin | разрушительное, идемпотентное | Remove a project from the manager's registry. dry_run:true returns the diff and saves nothing. |
| `add_session` | admin | идемпотентное | Add a session to a project, optionally with initial fields. dry_run:true returns the diff and saves nothing. |
| `update_session` | admin | идемпотентное | Change fields of a session: model, prompt, budget, permission mode, hooks and the rest of its config. dry_run:true returns the diff and saves nothing. |
| `remove_session` | admin | разрушительное, идемпотентное | Remove a session from its project's config. dry_run:true returns the diff and saves nothing. |
| `update_global_settings` | admin | идемпотентное | Change application-wide settings: budgets and alerts, permission handling, retry and log retention, language, cards. dry_run:true returns the diff and saves nothing. |
| `update_config` | admin | разрушительное, идемпотентное | Replace the whole application config with an edited copy of what get_config returned. |
| `init_git_repo` | admin | идемпотентное, запускает модель | Run git init in the project folder if it is not a repository yet, and make an initial commit if HEAD cannot be resolved. |
| `generate_claude_md` | admin | запускает модель | Create or re-point the Init session of a project and start it, so Claude writes (or refines) the project's CLAUDE.md. |
| `install_session_protocol` | admin | идемпотентное | Write the developer-session protocol (docs/git-workflow.md, scripts/worktree-pool.sh, the cm-task skills) into a project and gitignore its worktree pool. |
| `mirror_project` | admin | идемпотентное | Turn on "Mirror into Claude Manager" for a project and copy its settings and saved run logs now. |
| `unmirror_project` | admin | идемпотентное | Stop re-syncing a project's mirror. |
| `sync_project_mirrors` | operate | идемпотентное | Re-sync every mirrored project now. |
| `get_history` | read | только чтение | List the past runs of a project's sessions, or of all projects, newest first. |
| `search_session_log` | read | только чтение | Find log entries of a session's latest run by kind, tool or text, oldest first, one page at a time. |
| `get_session_metrics` | read | только чтение | Get a session's token counts, turns, cost, context use and duration. |
| `get_cost` | read | только чтение | Get the cost and token volume of one day across all projects, or of one project over a period. |
| `export_log` | operate | — | Write a session's latest log to a file in the manager's exports folder and return its path. |
| `import_project_logs` | operate | идемпотентное | Read a folder of saved CLI logs into the project's experience tables. |
| `get_project_log_files` | read | только чтение | List the session logs the manager saved automatically in a project's .claude-manager/logs folder, newest first. |
| `clear_project_logs` | admin | разрушительное, идемпотентное | Delete every saved log file of a project and its stored log lines. |
| `clean_old_logs` | admin | разрушительное, идемпотентное | Delete the stored log lines of runs older than a number of days, in every project. |
| `get_top_actions` | read | только чтение | List the tool calls a project's sessions repeat most, grouped by signature, with error rate and token weight. |
| `get_action_samples` | read | только чтение | List concrete calls that make up one action signature of a project. |
| `get_duration_profile` | read | только чтение | Show how long each kind of tool call takes in a project: median, 90th percentile, maximum, total and failure rate. |
| `get_token_attribution` | read | только чтение | Estimate which tool calls fill a project's context: tokens per signature, per tool and per result length. |
| `get_model_advice` | read | только чтение | Say whether automatic model routing is on and which model and effort it recommends for a configured session's prompt. |
| `session_preflight` | operate | запускает модель | Run the preflight analysis the settings of a session call for (what the Start button does) and return the plan to review, or nothing when the session should simply start. |
| `generate_roadmap` | operate | запускает модель | Split a whole project idea into a backlog of phases and tasks and save it as a draft roadmap plan. |
| `get_latest_draft_roadmap` | read | только чтение | Return the newest roadmap of a project that was generated but not approved yet. |
| `approve_roadmap` | admin | идемпотентное | Approve a draft roadmap: write ROADMAP.md and STATUS-P1.md into the project and create or update its P1 session. |
| `get_session_roadmap` | read | только чтение | Show the roadmap of a session as its task panel does: every phase and task with done, current or pending status. |
| `get_roadmap_task_detail` | read | только чтение | Read the task file (tasks/NN-*.md) a roadmap row links to. |
| `get_roadmap_row_detail` | read | только чтение | Read the long text of one roadmap row: its note column, else the raw row. |
| `get_mixed_quality` | read | только чтение | Compare the worker models of the mixed-programming tasks of a project: tasks done, rounds to green, clean patch rate and failures per worker. |
| `cancel_mixed_task` | operate | разрушительное, идемпотентное | Cancel a running mixed-programming task. |
| `get_skill_candidates` | read | только чтение | List the action patterns of a project's past runs that are worth turning into a skill, best first. |
| `distill_skill` | operate | запускает модель | Turn one skill candidate into a skill draft with a model run and save it as a draft; nothing is written into the project yet. |
| `get_skills` | read | только чтение | List the skills of a project in every status: drafts, trials, approved, rejected and archived. |
| `approve_skill` | admin | идемпотентное | Write a skill draft into the project as .claude/skills/<name>/SKILL.md so the agents load it. |
| `archive_skill` | operate | идемпотентное | Switch a skill off: an applied skill's folder is moved out of .claude/skills so the agents stop seeing it. |
| `restore_skill` | admin | идемпотентное | Bring an archived skill back into .claude/skills, or apply a rejected one anyway. |
| `get_skill_usage` | read | только чтение | Show how often agents actually load each skill of a project. |
| `get_skill_quality` | read | только чтение | Compare the runs before and after each approved skill of a project: does the skill help. |
| `get_skill_autopilot` | read | только чтение | Show whether the skill autopilot is on for a project, its daily budget and what it has spent. |
| `set_skill_autopilot` | admin | идемпотентное | Turn the skill autopilot on or off for a project and set its daily budget. |
| `run_skill_autopilot` | operate | запускает модель | Run one autopilot step for a project now instead of waiting for the next finished run. |

<!-- END GENERATED: tools -->

## Ошибки и траблшутинг

- `control-plane unreachable` — менеджер не запущен или `control.json`
  устарел/недоступен; запустите приложение либо задайте `CM_CONTROL_ADDR` и
  `CM_CONTROL_TOKEN`. Порт занят — `CM_CONTROL_PORT`.
- `requires access level admin … CM_MCP_LEVEL=admin` — инструмент выше уровня
  `cm-mcp`; повысить может только человек в конфиге клиента.
- `-32001` уровень, `-32003` самоповреждение (сессия пытается остановить
  себя; `--allow-self`). Остальные коды — «Ошибки и размер ответа».
- Инструмента нет в `tools/list` — он выше текущего уровня.
- Пустой или обрезанный ответ — страницы логов: `limit` и `next_cursor`.

## Реестр инструментов и тест паритета

Реализован в MCP-03. Каждый инструмент объявляется **один раз** в
`internal/control/mcptools.go` (`buildMCPTools`, обобщённый `define[In, Out]`):

- Go-структуры входа и выхода → `inputSchema` и `outputSchema`
  (`google/jsonschema-go`); описания полей — в теге `jsonschema:"…"`.
  Рукописных JSON-схем нет.
- `Title`, `Description`, `Annotations` (`readOnlyHint`, `destructiveHint`,
  `idempotentHint`, `openWorldHint`), `Level` (`read`/`operate`/`admin`;
  в `tools/list` уходит как `_meta["cm/accessLevel"]`, фильтрация — MCP-05),
  `Covers` — какие методы `App` покрывает инструмент.
- `Translate` (аргументы → вызов control-plane) и `Result`/`Summary`
  (ответ → `structuredContent` по `outputSchema` **и** короткая фраза в
  `content`, не дамп JSON). Списки оборачиваются в объект
  (`{"sessions": […]}`): `structuredContent` — всегда объект.
- Сессии адресуются только `session_id`.

Контракт фиксирует golden `testdata/mcp/tools.golden.json` (реальный
`tools/list`); намеренное изменение —
`go test ./cmd/cm-mcp -run TestToolsListGolden -update`. Корректность каждого
`structuredContent` проверяет `internal/control/mcptools_test.go`.

**Тест паритета** — `app_parity_test.go` (корень, пакет `main`): через
`reflect` перебирает экспортированные методы `*App`; каждый обязан быть в
`Covers` инструмента, в `parityExcluded` (с причиной) или в `parityPending`
(задача блока, которая его закроет). Метод из `parityPending`, уже покрытый
инструментом, роняет тест — список сокращается вместе с работой; новый
метод `App` без MCP не пройдёт гейт.

## Стандарт описаний

Введён в MCP-04; по нему делаются все инструменты MCP-06…11. Описание
собирает `Doc` в `mcptools_defs.go` в **одном порядке**, свободного текста нет:

> Что делает (1 фраза). **Use when:** когда звать. **Not for:** когда нет и какой
> инструмент вместо. **Returns:** что вернётся. **Side effects:** побочные
> эффекты, расход токенов или «none, read only». **Access:** уровень.
> **Example:** `{пример аргументов}`.

Уровень в тексте выводится из `Level`, пример проверяется по `inputSchema`.
Без внутреннего жаргона (`inputCh`, «атомики», «ring buffer»).

Параметры: у каждого `description` с форматом или примером; конечные
множества — `enum` (`scope`, `status`); значения по умолчанию в схеме
(`default`), границы (`minimum`/`maximum`) — через `spec.Input`.

Тест-линтер — `internal/control/mcplint_test.go`. Он падает на: описание не по
шаблону, чужой уровень доступа, жаргон, пример не по схеме; свойство без
`description`; «(default …)» в тексте без `default` в схеме; `status`/`scope`
без `enum`; ответ больше `MaxResponseBytes`. На реестре он зелёный; негативные
тесты на фикстурах доказывают, что каждое правило срабатывает.

## Ошибки и размер ответа

**Два канала.** Ошибка *вызова* (неизвестный инструмент, битый JSON-RPC,
аргументы не по схеме) — код JSON-RPC, это ловит go-sdk. Ошибка *выполнения*
инструмента — результат с `isError: true`: модель её видит и может
исправиться.

**Ошибка выполнения действенна:** что не так и что делать дальше.
`control.ExplainError` (`internal/control/mcperrors.go`) дополняет сообщение
приложения контекстом, запрашивая его через `get_sessions` /
`get_pending_permissions`:

| Было | Стало |
|---|---|
| `session "lumen/P9" not found` | сессия не найдена; в проекте `lumen`: `lumen/P1, lumen/P2`; все сессии — `get_sessions` (нет такого проекта → список проектов) |
| `no pending permission with id "r9"` | запрос не ожидает ответа; ожидают: `r1, r2` |
| `session … already running (status=working)` | уже запущена; `send_message` / `interrupt_session` / `stop_session` → `start_session` |
| `wait timeout` | условие не наступило; проверь `get_sessions`/`get_session_logs`, повтори с большим `timeout_ms` |
| `cursor "abc" …`, `limit 900 …`, `scope "x" …` | что передать вместо |

Списки в тексте ошибки — не больше 20 имён. Сообщения, которым помощь не
нужна, идут как есть, без префикса `rpc error -32000:`.

**Размер ответа.** Списковые инструменты (`get_sessions`,
`get_session_logs`, `get_pending_permissions`, `get_mixed_rounds`) принимают
`limit` (1–500, по умолчанию 100) и `cursor`. Ответ несёт `next_cursor` и
`truncated`; в тексте — подсказка `More available: call again with
cursor="N"`. Страница дополнительно режется до `MaxResponseBytes` (64 КиБ),
минимум одна запись. `cursor` — позиция от начала списка (для журнала —
от самой старой записи), его передают как есть из `next_cursor`.
Журнал читается страницами «старые → новые».

`approve_permission.scope` — `once` (по умолчанию) | `session` | `always`;
приложению уходит `allow` / `allow_session` / `allow_always`.

## Карта паритета

Каждый экспортированный метод `*App` (Wails привязывает один объект — это
полный список действий GUI) покрыт инструментом или исключён с причиной в
`parityExcluded` (`app_parity_test.go`): `PickDirectory`, `ShowMainWindow`,
`MinimizeToTray`, `Notify` — оконные/ОС-действия без смысла для агента;
`GetConfig` заменён `get_config`. Таблица ниже **генерируется** из реестра
(`Covers` каждого инструмента); править руками нельзя —
`go run ./cmd/cm-mcp -gen-docs` пересобирает её, тест `TestDocsInSync`
падает при расхождении.

<!-- BEGIN GENERATED: parity -->

| Метод `App` | Инструмент | Уровень |
|---|---|---|
| `AddPermissionRule` | `add_permission_rule` | admin |
| `AnswerQuestion` | `answer_question` | operate |
| `ApprovePlan` | `approve_plan` | admin |
| `ApproveRoadmap` | `approve_roadmap` | admin |
| `ApproveSkill` | `approve_skill` | admin |
| `ArchiveSkill` | `archive_skill` | operate |
| `CancelMixedTask` | `cancel_mixed_task` | operate |
| `CheckEnvironment` | `check_environment` | read |
| `CleanOldLogs` | `clean_old_logs` | admin |
| `ClearProjectLogs` | `clear_project_logs` | admin |
| `ClearSessionState` | `clear_session_state` | admin |
| `DispatchMixedTask` | `dispatch_mixed_task` | operate |
| `DistillSkill` | `distill_skill` | operate |
| `ExecutePlan` | `execute_plan` | operate |
| `ExportLog` | `export_log` | operate |
| `GenerateClaudeMdSession` | `generate_claude_md` | admin |
| `GenerateRoadmap` | `generate_roadmap` | operate |
| `GetActionSamples` | `get_action_samples` | read |
| `GetAllSessions` | `get_sessions` | read |
| `GetAutoModelRouting` | `get_model_advice` | read |
| `GetDailyCost` | `get_cost` | read |
| `GetDailyTokens` | `get_cost` | read |
| `GetDurationProfile` | `get_duration_profile` | read |
| `GetHistory` | `get_history` | read |
| `GetLatestDraftRoadmap` | `get_latest_draft_roadmap` | read |
| `GetMixedQuality` | `get_mixed_quality` | read |
| `GetMixedRounds` | `get_mixed_rounds` | read |
| `GetModelRecommendation` | `get_model_advice` | read |
| `GetPendingPermissions` | `get_pending` | read |
| `GetPendingPermissions` | `get_pending_permissions` | read |
| `GetPendingQuestions` | `get_pending` | read |
| `GetPendingQuestions` | `get_pending_questions` | read |
| `GetPermissionCandidates` | `get_permission_candidates` | read |
| `GetPlan` | `get_plan` | read |
| `GetProjectLogFiles` | `get_project_log_files` | read |
| `GetProjectMirror` | `get_project_setup` | read |
| `GetProjectTokens` | `get_cost` | read |
| `GetProjects` | `get_projects` | read |
| `GetRateLimitStatus` | `get_rate_limit` | read |
| `GetRoadmapRowDetail` | `get_roadmap_row_detail` | read |
| `GetRoadmapTaskDetail` | `get_roadmap_task_detail` | read |
| `GetSessionLog` | `get_session_logs` | read |
| `GetSessionMetrics` | `get_session_metrics` | read |
| `GetSessionRoadmap` | `get_session_roadmap` | read |
| `GetSessionState` | `get_session` | read |
| `GetSkillAutopilot` | `get_skill_autopilot` | read |
| `GetSkillCandidates` | `get_skill_candidates` | read |
| `GetSkillQuality` | `get_skill_quality` | read |
| `GetSkillUsage` | `get_skill_usage` | read |
| `GetSkills` | `get_skills` | read |
| `GetTokenAttribution` | `get_token_attribution` | read |
| `GetTopActions` | `get_top_actions` | read |
| `HasClaudeMd` | `get_project_setup` | read |
| `HasSessionProtocol` | `get_project_setup` | read |
| `ImportProjectLogs` | `import_project_logs` | operate |
| `InitGitRepo` | `init_git_repo` | admin |
| `InstallSessionProtocol` | `install_session_protocol` | admin |
| `InterruptSession` | `interrupt_session` | operate |
| `ListSlashCommands` | `list_slash_commands` | read |
| `MirrorProject` | `mirror_project` | admin |
| `RegisterMixedBrief` | `register_mixed_brief` | operate |
| `RespondPermission` | `deny_permission` | operate |
| `RespondPermission` | `approve_permission` | admin |
| `RestartSession` | `restart_session` | operate |
| `RestoreSkill` | `restore_skill` | admin |
| `ResumeSession` | `restart_session` | operate |
| `RunPreflight` | `run_preflight` | operate |
| `RunSkillAutopilot` | `run_skill_autopilot` | operate |
| `SendMessage` | `send_message` | operate |
| `SendMessageWithImages` | `send_message` | operate |
| `SessionPreflight` | `session_preflight` | operate |
| `SetSessionModel` | `set_session_model` | operate |
| `SetSessionRuntime` | `set_session_runtime` | operate |
| `SetSkillAutopilot` | `set_skill_autopilot` | admin |
| `StartAdHocChatSession` | `start_ad_hoc_chat` | operate |
| `StartProject` | `start_project` | operate |
| `StartSession` | `start_session` | operate |
| `StartSessionWithModel` | `start_session` | operate |
| `StopAll` | `stop_all_sessions` | admin |
| `StopProject` | `stop_project` | admin |
| `StopSession` | `stop_session` | operate |
| `SyncProjectMirrors` | `sync_project_mirrors` | operate |
| `UnmirrorProject` | `unmirror_project` | admin |

<!-- END GENERATED: parity -->


## Сессии

Область MCP-06: полный жизненный цикл сессии. Реализация — `internal/control/rpc_sessions.go`
(RPC) и `mcptools_defs.go` (инструменты); сессия везде адресуется `session_id = "project/session"`.

| Инструмент | Уровень | Что делает |
|---|---|---|
| `start_session` | operate | Запуск; необязательные `model`, `effort`, `runtime` (`claude`/`hermes`) — вместо отдельных инструментов. Модель и effort запоминаются как умолчания сессии, runtime пишется в конфиг |
| `start_ad_hoc_chat` | operate | Сессия `Chat` в проекте без task source и первого промпта |
| `stop_session` | operate | Жёстко или `after_task=true` (мягко) |
| `restart_session` | operate | Перезапуск; `resume=true` продолжает сохранённую беседу |
| `start_project` / `stop_project` | operate / admin | Все сессии проекта |
| `stop_all_sessions` | admin | Все сессии всех проектов; требует `confirm=true` |
| `set_session_model` | operate | Смена модели на лету (запущенная сессия перезапускается с resume) |
| `set_session_runtime` | operate | Смена CLI у остановленной сессии; у запущенной — отказ |
| `clear_session_state` | admin | Забыть сохранённую беседу: следующий старт — с чистого листа |
| `send_message` | operate | Текст и необязательные `images[]` |
| `get_session` | read | Всё для решения «что дальше»: `session` (статус, модель, effort, runtime, текущая задача, todo, токены), `saved_state`, `pending_permissions`, `pending_question` |
| `get_sessions` | read | Список, постранично |
| `list_slash_commands` | read | Slash-команды и скиллы сессии |
| `get_rate_limit` | read | Состояние rate limit аккаунта (`GetRateLimitStatus`) |

**Изображения.** Каждое — `path` (абсолютный путь к png/jpg/gif/webp на машине, где
запущен Claude Manager) **или** `data_base64` (+ `media_type`). Лимиты: не более
4 изображений на сообщение, не более 5 МиБ каждое (после декодирования); нарушение —
ошибка с подсказкой, сообщение не отправляется. Путь читает GUI-процесс, поэтому он
должен существовать там, а не в песочнице агента; для удалённого агента — base64.

**App или менеджер.** `set_session_runtime` и `start_ad_hoc_chat` требуют GUI-приложение
(правят конфиг); `start_session` с `model`/`effort`, `set_session_model`, `start_project`
идут через `App`, если он есть (запоминают выбор / порядок cache-affinity), иначе —
напрямую через менеджер (`playwright-server`).

**Что `get_session` не знает.** Отдельного поля «последняя ошибка» у сессии нет: при
`status = error` причина — в `get_session_logs`.

## Вопросы и permissions

Область MCP-07: всё, что ждёт человека. Реализация — `internal/control/rpc_interaction.go`
(RPC) и `mcptools_interaction.go` (инструменты).

| Инструмент | Уровень | Что делает |
|---|---|---|
| `get_pending` | read | Сводка «что ждёт человека»: `permissions`, `questions`, `total`; необязательный фильтр по сессии. Один вызов вместо двух |
| `get_pending_questions` | read | Вопросы сессий (`AskUserQuestion`, `clarify`, ```` ```ask-user ````, выбор менеджера при рестарте контекста), постранично |
| `answer_question` | operate | Ответ: один из `options` или свободный текст; сессия продолжает тот же процесс |
| `get_pending_permissions` | read | Запросы permissions, постранично |
| `approve_permission` | admin | `scope`: `once` / `session` / `always` (последние два добавляют постоянное правило) |
| `deny_permission` | operate | Отказ |
| `get_permission_candidates` | read | Кандидаты в правила из истории permissions за `days` (30): `safe` (read-only, готовы к `add_permission_rule`) и `needs_review`. Пусты, если `experience_tracking` был выключен |
| `add_permission_rule` | admin | Постоянное правило `allow`/`deny` для сессии: `tool`, `pattern`; повтор не дублируется |
| `get_plan` | read | Сохранённый план с подзадачами и прогрессом; `null`, если плана нет |
| `approve_plan` | admin | Сохраняет (отредактированный) план как одобренный; запуск — отдельно, `execute_plan` |

**Решение за человека — admin.** Одобрение permission, постоянное правило и одобрение
плана ставят агента на место человека, поэтому требуют `admin` (MCP-05); отказ и ответ
на вопрос — `operate`. Вопрос адресуется парой `session_id` + `question_id`
(id берётся из `get_pending_questions`).

**Планов в `get_pending` нет.** У плана нет очереди ожидания: он создаётся
`run_preflight`, читается `get_plan` по известному `plan_id`.

**App или менеджер.** `add_permission_rule` и `get_permission_candidates` читают
хранилище опыта и правят конфиг, поэтому нужны GUI-приложение; в `playwright-server`
они отвечают ошибкой «needs the Claude Manager app».

## Проекты и конфигурация

Область MCP-08: проекты, конфиг, действия над папкой проекта. Реализация —
`internal/control/rpc_projects.go` (RPC), `cfgops.go` (точечные правки, версия, маскировка),
`mcptools_projects.go` (инструменты). Конфиг читается и пишется в форме `config.toml`
(ключи из `config.example.toml`), а не в Go-именах полей GUI.

| Инструмент | Уровень | Что делает |
|---|---|---|
| `get_projects` | read | Проекты: `name`, `path`, имена сессий, флаги `mirror` и `mixed_programming`; постранично |
| `get_config` | read | Эффективный конфиг в форме `config.toml` без секретов + `version`; `section` отдаёт одну часть (`settings`, `optimization`, `project`, `worker`) |
| `check_environment` | read | Найденные программы (Claude CLI, Git, Python, сборка) и команда установки для недостающих |
| `get_project_setup` | read | Есть ли в папке `CLAUDE.md` и протокол сессий, состояние зеркала |
| `add_project` / `remove_project` | admin | Регистрация папки под новым именем / снятие с учёта. Путь должен существовать и быть абсолютным; удаление отказывает, пока в проекте идут сессии; файлы не трогаются |
| `update_project` | admin | Поля проекта по именам `config.toml` (`gates`, `mixed_programming`, `journal`, `mirror`, …); `name`, `path`, `session` не правятся |
| `add_session` / `remove_session` | admin | Сессия проекта, с начальными `fields`; удаление отказывает, пока сессия идёт |
| `update_session` | admin | Поля сессии (`model`, `prompt`, `max_budget_usd`, …); `null` сбрасывает поле |
| `update_global_settings` | admin | Белый список глобальных настроек: бюджеты и пороги, permissions, ретраи, язык, карточки. Пути к исполняемым файлам (`claude_path`, `hermes_path`) и `mirror_dir` — нет |
| `update_config` | admin | Конфиг целиком (то, что вернул `get_config`, после правки) с `version`; если конфиг изменился с тех пор — отказ «call get_config again» |
| `init_git_repo`, `generate_claude_md`, `install_session_protocol` | admin | Пишут в папку пользователя (`git init`, `CLAUDE.md` через сессию Init, файлы протокола) |
| `mirror_project` / `unmirror_project` | admin | Флаг зеркала проекта и немедленная синхронизация / снятие флага |
| `sync_project_mirrors` | operate | Пересинхронизировать все зеркала; проекты не меняются |

**Точечные правки.** Каждая (`add_*`, `update_*`, `remove_*`) работает на копии конфига,
проверяет её тем же `config.Validate`, что и загрузка с диска, возвращает diff
`changes: {поле: {was, now}}` и сохраняет через тот же `UpdateConfig`, что и GUI
(папка проекта получает свой оверлей). `dry_run: true` возвращает тот же diff и ничего не
пишет. Повтор правки, которая ничего не меняет, не пишет файл (`changed: false`).
Неизвестное поле — ошибка со списком допустимых; число там, где нужна строка — «a value has
the wrong type». Правки идут под одним замком сервера, поэтому два вызова агента не затирают
друг друга; против GUI защищает `version` у `update_config`.

`set_global_settings` (шлёт частичный объект как весь конфиг) удалён в MCP-18; правки — только `update_global_settings`.

**Секреты.** В конфиге их нет: воркер называет переменную окружения ключа (`key_env`), сам
ключ не хранится. `get_config` всё равно маскирует значения под ключами `api_key`, `secret`,
`token`, `password` — на случай полей, которые появятся позже.

**`PickDirectory`** в MCP нет: путь передаётся аргументом `path` у `add_project`; несуществующий,
относительный или файловый путь отвечает понятной ошибкой до обращения к менеджеру.

**App или менеджер.** Действия над папкой и зеркалом нужны GUI-приложению; в `playwright-server`
они отвечают ошибкой «needs the Claude Manager app». Правки конфига работают с любым сервером,
у которого есть `AppAPI`.

## Логи и метрики

Область MCP-09: логи сессий и проектов, расход, таблицы слоя опыта. Реализация —
`internal/control/rpc_logs.go` (RPC), `mcptools_logs.go` (инструменты). Каждый список режется
страницей и по байтам (`MaxResponseBytes`), в ответе `next_cursor` и `truncated`.

| Инструмент | Уровень | Что делает |
|---|---|---|
| `get_session_logs` | read | Лог последнего запуска сессии по порядку, страницами |
| `search_session_log` | read | Записи лога по `level` (`text`, `tool`, `error`, `result`, `system`), `tool` и подстроке `contains`; нужен хотя бы один фильтр. Поиск идёт на стороне менеджера: агент не выкачивает весь лог |
| `get_history` | read | Прошлые запуски проекта (или всех), новые первыми: статус, стоимость, токены |
| `get_session_metrics` | read | Токены, ходы, стоимость, заполнение контекста, длительность одной сессии |
| `get_cost` | read | `scope=day` (дата `YYYY-MM-DD`, по умолчанию сегодня, все проекты) или `scope=project` (`project`, `period` дней): `cost_usd` и токены |
| `get_rate_limit` | read | Статус rate-limit аккаунта (бывший `get_metrics` без проекта) |
| `export_log` | operate | Пишет лог сессии в файл `~/.claude-manager/exports/<сессия>-<время>.<md\|json\|txt>` и возвращает путь, а не содержимое |
| `import_project_logs` | operate | Читает папку сохранённых логов CLI (по умолчанию `.claude-manager/logs` проекта) в таблицы опыта; повторный импорт папки пропускает уже учтённое |
| `get_project_log_files` | read | Автосохранённые логи проекта: имя, путь, размер, время |
| `clear_project_logs` | admin | Удаляет файлы логов проекта и их строки в базе; история запусков остаётся. Нужен `confirm: true` |
| `clean_old_logs` | admin | Удаляет строки логов старше `retention_days` во всех проектах. Нужен `confirm: true` |
| `get_top_actions` | read | Самые частые сигнатуры вызовов инструментов проекта за `days` |
| `get_action_samples` | read | Конкретные вызовы за одной сигнатурой (`signature` из `get_top_actions`) |
| `get_duration_profile` | read | Медиана, p90, максимум, сумма и доля отказов по сигнатурам |
| `get_token_attribution` | read | Оценка токенов контекста по сигнатурам, инструментам и длине результата; `top_n` самых дорогих |
| `get_model_advice` | read | Включена ли авто-маршрутизация моделей и какую модель/усилие она советует для промпта сессии |

**Новые RPC.** `GetDailyTokens`, `GetProjectTokens`, `SearchSessionLog`, `GetModelAdvice`
добавлены в control-plane; `ExportLog` в RPC — не диалог `App.ExportLog`, а запись файла
в папку экспорта. `get_cost` вызывает токенные методы: их `cost_usd` берётся из тех же строк
`daily_metrics`, что и `GetDailyCost`.

**Таблицы опыта пусты, пока выключен `experience_tracking`** в настройках проекта
(`get_top_actions`, `get_action_samples`, `get_duration_profile`, `get_token_attribution`).

**App или менеджер.** Файлы логов, очистка, импорт, таблицы опыта и совет по модели нужны
GUI-приложению; в `playwright-server` они отвечают ошибкой «needs the Claude Manager app».
Логи, поиск, экспорт, история и расход работают с любым менеджером.

## Ожидание и события

Область MCP-12. Реализация — `internal/control/wait.go` (`POST /wait`), `emit.go` (`EventNames`),
`cmd/cm-mcp/server.go` (progress, таймаут долгих инструментов).

| Инструмент | Что ждёт |
|---|---|
| `wait_for_status` | Статус одной сессии |
| `wait_for_event` | Событие по имени и полям `match`; `event` — `enum` из `control.EventNames` (те же константы, что эмитят `session`/`worker`) |
| `wait_for_worker_status` | Конец mixed-задачи (`worker:done`) |
| `wait_for_any` | Первое из событий «нужен человек» по набору `session_ids`: вопрос, permission, ошибка, конец задачи или хода (`session:result`), статус `waiting_permission` / `waiting_for_user` / `error`. Уже висящий вопрос, permission или такой статус засчитывается сразу (читается состояние менеджера, не кольцо событий — старый ответ не сработает повторно) |

Цикл агента-оператора без опроса: `wait_for_any` → разобрать пришедшее (`data.id` — сессия) → ответить
(`answer_question`, `approve_permission`, `send_message`) → снова `wait_for_any`.

- **Таймаут.** По умолчанию 5 с (`wait_for_worker_status`, `wait_for_any` — 60 с), максимум 10 минут
  (`MaxWaitTimeout`, `maximum` в схеме; больший `timeout_ms` обрезается, не отвергается).
- **Истечение — результат, не ошибка.** `{reached: false, timeout_ms, current_status, statuses, hint}`:
  `current_status` — статус ожидаемой сессии сейчас, `statuses` — по каждой сессии `wait_for_any`,
  `hint` — «вызови снова». При успехе `reached: true` и `event`/`data`/`ts`.
- **Progress.** Если вызов `tools/call` несёт `_meta.progressToken`, долгий инструмент (`wait_for_*`,
  `run_preflight`, `session_preflight`, `generate_roadmap`, `distill_skill`) раз в 10 с шлёт
  `notifications/progress`: `progress` — прошедшие секунды (общее неизвестно), `message` — «still running».
  Без токена уведомлений нет.
- **Отмена.** `notifications/cancelled` отменяет контекст вызова; HTTP-запрос к control-plane обрывается, и
  `handleWait` по `r.Context().Done()` выходит и снимает подписку на события (тест
  `TestWait_CancelReleasesSubscription`, `TestProtocol_CancelAbortsWait`).
- **Верхняя граница инструментов, что запускают модель** (`session_preflight`, `run_preflight`,
  `generate_roadmap`, `distill_skill`): 15 минут (`LongToolTimeout`); по истечении — ошибка с подсказкой, что
  операция продолжается в приложении и результат смотреть через `get_*`.
- **Подписки.** `notifications/resources/updated` не реализованы: ресурсов `cm://sessions/…` ещё нет (MCP-13),
  и подписываться не на что. До тех пор живые обновления — `wait_for_*`.

## Планирование и mixed

Область MCP-10: preflight, roadmap и mixed programming. Реализация — `internal/control/rpc_planning.go`
(RPC roadmap), `mcptools_planning.go` (новые инструменты); остальные инструменты области
(`run_preflight`, `execute_plan`, `register_mixed_brief`, `dispatch_mixed_task`, `get_mixed_rounds`) появились раньше.
Долгие операции (`session_preflight`, `generate_roadmap`, `dispatch_mixed_task`) пока отвечают, когда модель закончила;
прогресс, отмена и таймаут — в «Ожидание и события».

| Инструмент | Уровень | Что делает |
|---|---|---|
| `session_preflight` | operate | Тот же гейт, что у кнопки Start: `needed=false`, если сессии хватает обычного старта, иначе план для просмотра |
| `run_preflight` / `execute_plan` | operate | План для ad-hoc задачи и его запуск (`approve_plan` — в «Вопросы и permissions») |
| `generate_roadmap` | operate | Разбивает идею проекта на фазы и задачи, сохраняет черновик (`plan_id`); стоит денег, в проект ничего не пишет |
| `get_latest_draft_roadmap` | read | Последний неодобренный черновик проекта (`found=false`, если нет) |
| `approve_roadmap` | admin | Пишет `ROADMAP.md` и `STATUS-P1.md` в проект, создаёт или обновляет сессию P1. Существующие файлы без `overwrite=true` не трогает |
| `get_session_roadmap` | read | Дерево roadmap как в панели задач, плоским списком в порядке чтения: у узла `depth`, `status`, `current`, `detail_path`, `line`; страницами |
| `get_roadmap_task_detail` | read | Текст файла `tasks/NN-*.md` (`path` — `detail_path` узла); окна по `MaxResponseBytes`, `cursor` — смещение в байтах |
| `get_roadmap_row_detail` | read | Длинный текст строки roadmap (`roadmap_file` и `line` узла) |
| `register_mixed_brief` / `dispatch_mixed_task` / `get_mixed_rounds` | operate / read | Бриф для worker-модели, запуск цикла «патчи → ворота → фидбэк», сохранённые раунды |
| `get_mixed_quality` | read | Сравнение worker-моделей проекта: задачи, раунды до зелёного, доля чистых патчей, сбои |
| `cancel_mixed_task` | operate | Отменяет идущую задачу (`task_id` вида `project/brief/worker`); уже применённые патчи остаются |

**App или менеджер.** `approve_roadmap` и чтение roadmap нужны GUI-приложению (конфиг и папка проекта);
в `playwright-server` они отвечают ошибкой «needs the Claude Manager app». Остальное работает с любым менеджером.

## Навыки

Область MCP-11: experience layer (кандидаты в навыки, их дистилляция, применение, автопилот). Реализация —
`internal/control/rpc_skills.go` (RPC), `mcptools_skills.go` (инструменты). Окон, ожидающих решения, нет:
`parityPending` для этой области пуст.

**Флаги.** MCP ничего не включает в обход флагов, а сообщает, какой нужен. Кандидаты берутся из истории
действий, которая пишется только при `[optimization] experience_tracking = true`; с выключенным флагом или
короткой историей `get_skill_candidates` отвечает пустым списком, а сводка называет флаг. Автопилот работает
только при `auto_skills` проекта: `run_skill_autopilot` при выключенном отвечает ошибкой с подсказкой
`set_skill_autopilot`. Единственный явный способ включить — `set_skill_autopilot` (admin); он же включает
`experience_tracking`. Ничего не пишется в репозиторий без `approve_skill` / `restore_skill` (admin) или
включённого автопилота.

| Инструмент | Уровень | Что делает |
|---|---|---|
| `get_skill_candidates` | read | Повторяющиеся паттерны действий проекта, лучшие первыми; `sig` кандидата идёт в `distill_skill`. Страницами |
| `distill_skill` | operate | Запускает модель и сохраняет черновик навыка (`status=draft`) с `id` и `md`; кандидат ищется по `sig`, ворота берутся из конфига проекта. Стоит денег, в проект ничего не пишет; ниже порога — ошибка со счётом |
| `get_skills` | read | Навыки проекта во всех статусах (draft, trial, approved, rejected, archived); фильтр `status`, текст `md` только с `include_md=true`. Страницами |
| `approve_skill` | admin | Пишет `.claude/skills/<name>/SKILL.md`; без `md` берётся сохранённый черновик. С `auto_skills` навык идёт в испытание, иначе одобрен. Существующий файл без `overwrite=true` не трогает |
| `archive_skill` | operate | Убирает папку навыка из `.claude/skills` в `.claude-manager/archived-skills` |
| `restore_skill` | admin | Возвращает архивный навык (или применяет отклонённый); `overwrite` как у `approve_skill` |
| `get_skill_usage` | read | Сколько раз агенты загружали каждый навык |
| `get_skill_quality` | read | Прогоны до и после одобрения навыка; флаги `insufficient_data` и `stale` |
| `get_skill_autopilot` | read | `enabled` (auto_skills), `tracking` (experience_tracking), бюджет и расход |
| `set_skill_autopilot` | admin | Включает или выключает автопилот проекта и задаёт дневной бюджет (0 — без лимита) |
| `run_skill_autopilot` | operate | Один шаг автопилота сейчас; отчёт о применённых, оставленных, архивированных и отклонённых |

**App или менеджер.** Все инструменты области нужны GUI-приложению (хранилище навыков, дистиллятор, автопилот);
в `playwright-server` они отвечают ошибкой «needs the Claude Manager app». Идентификатор навыка `skill_id` —
строка или число.

## Ресурсы

Область MCP-13. Реализация — `cmd/cm-mcp/resources.go`. Ресурсы только читают; тело каждого строится теми же
инструментами и тем же кодом `ShapeResult`, что и `tools/call`, поэтому ресурс не показывает больше, чем
read-инструмент (ни токена control-plane, ни секретов конфига). `resources/list` и
`resources/templates/list` постраничные средствами SDK; неизвестный URI — JSON-RPC ошибка «resource not found».

| URI | Данные | Источник |
|---|---|---|
| `cm://guide` | Это руководство (`text/markdown`). Встроено в бинарник из `docs/mcp.md` через пакет `docs` — второй копии нет | — |
| `cm://projects` | Проекты, первая страница | `get_projects` |
| `cm://projects/{project}/config` | Настройки одного проекта и его сессий без секретов, с `version` | `get_config` (`section=project`) |
| `cm://sessions` | Сессии со статусом и метриками, первая страница | `get_sessions` |
| `cm://sessions/{project}/{session}` | Полное состояние сессии | `get_session` |
| `cm://sessions/{project}/{session}/log{?cursor}` | Страница лога; `next_cursor` предыдущей страницы — в `?cursor=` | `get_session_logs` |

У каждого ресурса есть `name`, `title`, `description`, `mimeType` (`application/json`, кроме гайда).
`instructions` отсылает к `cm://guide`. Capability `resources` объявлена без `listChanged`/`subscribe`.

## Промпты

Готовые сценарии для пользователя клиента (`prompts/list`, `prompts/get`); реализация —
`cmd/cm-mcp/prompts.go`. Промпт только отдаёт сообщение, а вызовы делает модель клиента — с правами
текущего уровня доступа. Тест проверяет, что каждый названный в тексте инструмент есть в реестре.

| Промпт | Аргументы | Что ведёт по шагам |
|---|---|---|
| `setup_project` | `path`*, `name` | `check_environment` → `add_project` → `get_project_setup` → `generate_claude_md`/`install_session_protocol` → `add_session` → `start_session` |
| `run_task` | `session_id`*, `task`* | `get_session` → `send_message` → цикл `wait_for_any` с `approve_permission`/`deny_permission`/`answer_question` → `get_session_logs`, `get_cost` |
| `diagnose_session` | `session_id`* | `get_session` → `get_session_logs`/`search_session_log` → `get_history` → один следующий шаг |
| `triage_waiting` | — | `get_pending` → решение по каждому → `approve_permission`/`deny_permission`/`answer_question` |
| `cost_report` | `days`, `project` | `get_cost` (project, day) ; `get_rate_limit` |

`*` — обязательный аргумент; пропуск — ошибка.

## Проверка

Соответствие спецификации и сквозной сценарий входят в обычный `go test ./cmd/cm-mcp/`:

- `conformance_test.go` собирает настоящий `cm-mcp`, запускает его подпроцессом по stdio и проходит
  `initialize`, `ping`, `tools/list` и `tools/call`, `resources/list`, `resources/templates/list`,
  `resources/read`, `prompts/list`, `prompts/get`. Методы необъявленных возможностей
  (`logging/setLevel`, `completion/complete`, `tasks/*`, `resources/subscribe`) отвечают `-32601`.
- `e2e_test.go` поднимает настоящий control-plane с `fakeclaude` и ведёт всё только через MCP
  (клиент из SDK): пустой конфиг → `add_project` → `add_session` → `start_session` → вопрос и
  `answer_question` → `approve_permission` → логи и метрики → `restart_session` →
  `set_session_model` → `stop_project` → `remove_session` → `remove_project`. Сценарий fakeclaude —
  `testdata/scenarios/mcp-full-control.json`.

### Оценка реальным агентом

Opt-in (`CM_MCP_EVAL=1`), тратит токены API. `cmd/cm-mcp-eval` на каждую из 13 задач поднимает свежий
экземпляр менеджера (настоящие `SessionManager`, хранилище и control-plane на `fakeclaude`) и запускает
`claude -p` в пустой папке только с сервером `cm-mcp` (`--strict-mcp-config`, `--tools ""`): ни
репозитория, ни встроенных инструментов, ни `docs/mcp.md`. Агент знает только `instructions`,
`cm://guide` и описания инструментов. Исход проверяется **по состоянию менеджера** (конфиг, статусы,
очереди permissions и вопросов, файлы roadmap, решения в `permission_events`); у задач «узнай и скажи»
агент пишет строку `ANSWER: …`, которую сверяют со значением из состояния.

```bash
CM_MCP_EVAL=1 go run ./cmd/cm-mcp-eval -model sonnet -out testdata/mcp-eval/last-run.md
CM_MCP_EVAL=1 go run ./cmd/cm-mcp-eval -list                 # задачи, области, уровни
CM_MCP_EVAL=1 go run ./cmd/cm-mcp-eval -only remove-project  # одна задача
```

Задачи и проверки — `cmd/cm-mcp-eval/tasks.go`. Без модели те же задачи гоняет обычный
`go test ./cmd/cm-mcp-eval`: скриптовый «агент» (`solutions.go`) решает каждую через `cm-mcp`, а проверка
должна быть красной до его действий и зелёной после — так ловятся нерешаемая задача и пустая проверка.
Экземпляр не умеет то, что требует GUI (`ProjectsApp` и часть `LogsApp`/`SkillsApp` отвечают
«not available in the evaluation instance»), поэтому задачи на эти методы не входят. Область
mixed-программирования (`get_mixed_quality` и др.) в оценку не вошла: нужен `fakeworker` и заранее
посеянные задачи — это отдельная доработка.

Метрики на задачу: успех, вызовы инструментов, вызовы с `is_error`, ходы, токены (вход + выход + кэш),
стоимость. Результат прогона 2026-10-15 (`sonnet`, по одному прогону на задачу, `testdata/mcp-eval/last-run.md`):

| Task | Area | OK | Calls | Failed calls | Turns | Tokens | Cost, USD |
|---|---|---|---|---|---|---|---|
| setup-from-scratch | sessions+config | yes | 5 | 0 | 6 | 216446 | 0.065 |
| run-and-report-cost | sessions+metrics | yes | 2 | 0 | 3 | 79168 | 0.119 |
| answer-question | interaction | yes | 2 | 0 | 3 | 79286 | 0.030 |
| approve-one-permission | interaction | yes | 3 | 0 | 4 | 107113 | 0.036 |
| switch-model-and-restart | sessions | yes | 3 | 0 | 4 | 106392 | 0.037 |
| stop-project | sessions | yes | 1 | 0 | 2 | 70701 | 0.025 |
| update-session-config | projects+config | yes | 2 | 0 | 3 | 107223 | 0.037 |
| remove-project | projects+config | yes | 1 | 0 | 2 | 70795 | 0.025 |
| find-failed-session | logs | yes | 2 | 0 | 3 | 55211 | 0.086 |
| project-cost | metrics | yes | 1 | 0 | 2 | 35186 | 0.019 |
| generate-and-approve-roadmap | planning | yes | 3 | 0 | 4 | 146522 | 0.051 |
| skill-candidates | skills | yes | 1 | 0 | 2 | 35300 | 0.019 |
| wait-for-permission | events | yes | 2 | 0 | 3 | 79656 | 0.031 |

Success: 13/13 (100%). Failed calls per task: 0.00. Calls per task: 2.2. Tokens per task: 91461. Cost: $0.58.

**До/после правок.** Правок описаний и ошибок по итогам прогона не потребовалось: первый же прогон дал
13/13 и 0 ошибочных вызовов, так что колонки «после» нет — «до» и есть итог. Критерий закрытия
(успех ≥ 90%, ошибочных вызовов < 1 на задачу) выполнен. Оговорка: одна модель, один прогон на задачу и
задачи в основном прямолинейные; при изменении описаний инструментов прогон стоит повторить и
сравнить с этой таблицей.

**Находка по ходу.** Три сессии, запущенные в один и тот же момент, теряют строки запусков в
`session_runs` (в базе остаётся одна), и лог упавшей сессии после этого недоступен через
`get_session_logs`. Менеджер не правился; задачи оценки поэтому запускают сессии по одной.

### MCP Inspector

Вручную (нужны Node/npx и сеть при первом запуске) официальный
[MCP Inspector](https://github.com/modelcontextprotocol/inspector) в CLI-режиме:

```bash
scripts/mcp-inspect.sh                                   # tools, resources, templates, prompts
scripts/mcp-inspect.sh tools/call --tool-name get_sessions
```

Скрипт собирает `cm-mcp` во временную папку. Запущенное приложение подхватывается из
`~/.claude-manager/control.json` (или `CM_CONTROL_ADDR` / `CM_CONTROL_TOKEN`); без него списки
работают, а `tools/call` вернёт «control-plane unreachable».
