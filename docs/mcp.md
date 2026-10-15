# Claude Manager как MCP-сервер

Руководство и решения блока `MCP-TASKS.md`. Файл создан в MCP-01 и растёт
с каждой задачей блока: MCP-02 добавляет «Протокол», MCP-06…11 — разделы
областей, MCP-15 — онбординг. Раздел «Карта паритета» — источник, из
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
  этот файл (ресурс `cm://guide` — MCP-13).
- `capabilities`: только `tools` (без `listChanged`: набор фиксирован) —
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

| Инструмент сейчас | Старые аргументы | Новые аргументы | Алиас в блоке |
|---|---|---|---|
| `start_session`, `stop_session`, `interrupt_session`, `restart_session` | `project`, `session` | `session_id` | да: принимаются оба вида; если пришли оба — ошибка «передайте либо `session_id`, либо `project`+`session`» |
| `send_message` | `project`, `session`, `text` | `session_id`, `text` | да |
| `get_session_logs` | `project`, `session`, `tail` | `session_id`, `limit`, `cursor` (`tail` — алиас `limit`) | да |
| `wait_for_status` | `project`, `session`, `status`, `timeout_ms` | `session_id`, … | да |
| `approve_permission`, `deny_permission` | `session_id`, `request_id`, `scope` | без изменений | — |
| `run_preflight`, `get_mixed_rounds`, `dispatch_mixed_task` | `project` | `project` | — (проектные) |
| `wait_for_event` | `event`, `match` | без изменений (`match` может содержать `session_id`) | — |
| `get_metrics` | `project`, `period` | `get_cost`: `scope`, `project`, `days` | алиас `get_metrics` (`deprecated` в описании) |
| `set_global_settings` | `config` | `update_global_settings` | алиас `set_global_settings` |

Алиасы живут до MCP-17; удаление — отдельная задача после блока. Ответы
**всегда** содержат `session_id` (а не пару полей), чтобы агент передавал его
дальше как есть. Идентификаторы без проекта (`plan_id`, `request_id`,
`question_id`, `skill_id`, `task_id`) — непрозрачные значения из ответов;
агент их не конструирует.

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
несёт его в заголовке `X-CM-Level`), чтобы урезанный `cm-mcp` нельзя было
обойти прямым вызовом `/rpc`. Тестовая обвязка GUI (`docs/testing-harness.md`) запускает `cm-mcp` с
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
- **Граница.** `cm-mcp` всегда шлёт `X-CM-Level`; запрос **без** заголовка — прямой
  вызов держателя токена (тестовая обвязка, e2e-раннер) и считается `admin`, как
  до введения уровней. Токен остаётся полномочием: тот, кто его прочитал из
  `~/.claude-manager/control.json` (файл 0600), может заявить любой уровень.
  Уровни защищают от ошибок и обрезают права *агента через cm-mcp*, а не от
  процесса с доступом к файлу токена.
- **Самоповреждение.** Менеджер кладёт в окружение каждой сессии
  `CM_SESSION_ID=project/session`; `cm-mcp` пересылает его (`X-CM-Caller-Session`).
  `StopSession`/`RestartSession`/`ClearSessionState` своей сессии, `StopProject`
  своего проекта и `StopAll` отклоняются, пока человек не задал
  `CM_MCP_ALLOW_SELF=1` (или `--allow-self`).
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
- Сессии адресуются `session_id`; пара `project`+`session` — алиас (оба
  сразу — ошибка).

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
Журнал читается страницами «старые → новые»; параметр `tail` — устаревший
синоним `limit`.

`approve_permission.scope` — `once` (по умолчанию) | `session` | `always`;
приложению уходит `allow` / `allow_session` / `allow_always`.

## Карта паритета

Таблица охватывает **все 87 экспортированных методов `*App`**
(`grep "^func (a \*App) [A-Z]" app*.go`: 83 в `app.go`, 4 в
`app_mirror.go`; в постановке блока значилось 84 — картина на 2026-10-13
устарела). Wails привязывает один объект — `app` (`main.go`, `Bind`), то
есть список методов `App` = полный список действий GUI, идущих через Go.

**RPC сейчас** — есть ли метод в `internal/control/rpc.go:buildRegistry`
(33 из 87; ещё два RPC, `GetSession` и `StartSessionWithOverride`, — не
методы `App`). **Задача** — кто переносит. Один инструмент может покрывать
несколько методов (`get_cost` → `GetDailyCost`, `GetDailyTokens`,
`GetProjectCost`, `GetProjectTokens`). Итог: 83 метода → MCP, 4 → `excluded`.
Уровни: 40 `read`, 24 `operate`, 19 `admin` (для `RespondPermission` —
`approve` = admin, `deny` = operate; считается по первому).

Аннотации: *только чтение* = `readOnlyHint`; *разрушительное* =
`destructiveHint`; *идемпотентное* = `idempotentHint`; *аддитивное* — не
читает и не разрушает (`readOnlyHint=false`, `destructiveHint=false`).
`openWorldHint=true` только у инструментов, запускающих модель (сессии,
preflight, roadmap, distill); остальные — `false`.

| Метод `App` | Определён | RPC сейчас | Задача | MCP-инструмент | Уровень | Аннотации | Заметка |
|---|---|---|---|---|---|---|---|
| `GetConfig` | `app.go:379` | да | MCP-08 | `get_settings` | read | только чтение | секреты вырезаются |
| `GetProjects` | `app.go:384` | нет | MCP-08 | `list_projects` | read | только чтение | — |
| `CheckEnvironment` | `app.go:395` | нет | MCP-08 | `check_environment` | read | только чтение | claude/git/hermes в PATH |
| `GetAutoModelRouting` | `app.go:404` | нет | MCP-09 | `get_model_advice` | read | только чтение | поле `auto_routing` |
| `GetModelRecommendation` | `app.go:415` | нет | MCP-09 | `get_model_advice` | read | только чтение | — |
| `StartSessionWithModel` | `app.go:461` | нет | MCP-06 | `start_session` | operate | идемпотентное | аргументы `model`,`effort` тула start_session |
| `SessionPreflight` | `app.go:475` | да | MCP-10 | `session_preflight` | operate | аддитивное | тот же гейт, что у кнопки Start |
| `RunPreflight` | `app.go:481` | да | MCP-10 | `run_preflight` | operate | аддитивное | существующий |
| `ApprovePlan` | `app.go:487` | да | MCP-07 | `approve_plan` | admin | аддитивное | одобрение плана вместо человека |
| `ExecutePlan` | `app.go:493` | да | MCP-07 | `execute_plan` | operate | аддитивное | существующий |
| `GetPlan` | `app.go:498` | да | MCP-07 | `get_plan` | read | только чтение | — |
| `GenerateRoadmap` | `app.go:505` | нет | MCP-10 | `generate_roadmap` | operate | аддитивное | запускает модель (стоит денег) |
| `GetLatestDraftRoadmap` | `app.go:514` | нет | MCP-10 | `get_latest_draft_roadmap` | read | только чтение | — |
| `ApproveRoadmap` | `app.go:524` | нет | MCP-10 | `approve_roadmap` | admin | аддитивное | пишет ROADMAP/задачи в репозиторий |
| `InstallSessionProtocol` | `app.go:599` | нет | MCP-08 | `install_session_protocol` | admin | идемпотентное | пишет скрипты/скиллы в репозиторий |
| `HasSessionProtocol` | `app.go:607` | нет | MCP-08 | `get_project_status` | read | только чтение | поле `has_session_protocol` |
| `HasClaudeMd` | `app.go:614` | нет | MCP-08 | `get_project_status` | read | только чтение | поле `has_claude_md` |
| `GenerateClaudeMdSession` | `app.go:629` | нет | MCP-08 | `generate_claude_md` | admin | аддитивное | запускает сессию, пишущую CLAUDE.md в репозиторий |
| `StartAdHocChatSession` | `app.go:686` | нет | MCP-06 | `start_ad_hoc_chat` | operate | аддитивное | ad-hoc сессия проекта |
| `RegisterMixedBrief` | `app.go:742` | да | MCP-10 | `register_mixed_brief` | operate | аддитивное | существующий |
| `DispatchMixedTask` | `app.go:750` | да | MCP-10 | `dispatch_mixed_task` | operate | аддитивное | существующий |
| `GetMixedRounds` | `app.go:755` | да | MCP-10 | `get_mixed_rounds` | read | только чтение | существующий |
| `GetMixedQuality` | `app.go:761` | да | MCP-10 | `get_mixed_quality` | read | только чтение | — |
| `CancelMixedTask` | `app.go:766` | да | MCP-10 | `cancel_mixed_task` | operate | разрушительное | — |
| `StartSession` | `app.go:772` | да | MCP-06 | `start_session` | operate | идемпотентное | существующий; +`model`,`effort` |
| `StopSession` | `app.go:776` | да | MCP-06 | `stop_session` | operate | идемпотентное | существующий; `after_task` = soft |
| `InterruptSession` | `app.go:782` | да | MCP-06 | `interrupt_session` | operate | идемпотентное | существующий |
| `StopAll` | `app.go:786` | да | MCP-06 | `stop_all_sessions` | admin | разрушительное | останавливает чужую работу; требует `confirm=true` |
| `RestartSession` | `app.go:790` | да | MCP-06 | `restart_session` | operate | аддитивное | существующий, `resume=false` |
| `ResumeSession` | `app.go:794` | да | MCP-06 | `restart_session` | operate | аддитивное | существующий, `resume=true` |
| `ClearSessionState` | `app.go:800` | да | MCP-06 | `clear_session_state` | admin | разрушительное | стирает сохранённый CLI session ID |
| `GetSessionState` | `app.go:808` | да | MCP-06 | `get_session` | read | только чтение | сохранённое состояние — поле `saved_state` ответа get_session |
| `InitGitRepo` | `app.go:820` | нет | MCP-08 | `init_git_repo` | admin | идемпотентное | пишет в папку пользователя |
| `StartProject` | `app.go:833` | да | MCP-06 | `start_project` | operate | идемпотентное | все сессии проекта |
| `StopProject` | `app.go:875` | да | MCP-06 | `stop_project` | admin | разрушительное | останавливает все сессии проекта |
| `SendMessage` | `app.go:881` | да | MCP-06 | `send_message` | operate | аддитивное | существующий |
| `SendMessageWithImages` | `app.go:887` | нет | MCP-06 | `send_message` | operate | аддитивное | аргумент `images[]` (base64) к send_message |
| `ListSlashCommands` | `app.go:893` | да | MCP-06 | `list_slash_commands` | read | только чтение | — |
| `RespondPermission` | `app.go:897` | да | MCP-07 | `approve_permission` / `deny_permission` | admin (approve) / operate (deny) | аддитивное | существующие; approve — вместо человека |
| `GetPendingPermissions` | `app.go:901` | да | MCP-07 | `get_pending_permissions` | read | только чтение | существующий |
| `AnswerQuestion` | `app.go:905` | нет | MCP-07 | `answer_question` | operate | аддитивное | ответ на вопрос сессии (AskUserQuestion) |
| `GetPendingQuestions` | `app.go:909` | нет | MCP-07 | `get_pending_questions` | read | только чтение | — |
| `SetSessionModel` | `app.go:922` | нет | MCP-06 | `set_session_model` | operate | идемпотентное | — |
| `SetSessionRuntime` | `app.go:1002` | нет | MCP-06 | `set_session_runtime` | operate | идемпотентное | claude / hermes; отказ, пока сессия запущена |
| `GetAllSessions` | `app.go:1063` | да | MCP-06 | `get_sessions` | read | только чтение | существующий; курсор + фильтры |
| `GetSessionLog` | `app.go:1072` | да | MCP-09 | `get_session_logs` | read | только чтение | существующий; курсор вместо offset/limit |
| `GetHistory` | `app.go:1076` | да | MCP-09 | `get_history` | read | только чтение | — |
| `GetSessionMetrics` | `app.go:1080` | да | MCP-09 | `get_session_metrics` | read | только чтение | — |
| `GetDailyCost` | `app.go:1084` | да | MCP-09 | `get_cost` | read | только чтение | scope=day |
| `GetDailyTokens` | `app.go:1088` | нет | MCP-09 | `get_cost` | read | только чтение | scope=day, токены |
| `GetProjectCost` | `app.go:1092` | да | MCP-09 | `get_cost` | read | только чтение | scope=project; существующий get_metrics — алиас |
| `GetProjectTokens` | `app.go:1096` | нет | MCP-09 | `get_cost` | read | только чтение | scope=project, токены |
| `GetRateLimitStatus` | `app.go:1100` | да | MCP-09 | `get_rate_limit_status` | read | только чтение | — |
| `UpdateConfig` | `app.go:1112` | да | MCP-08 | `update_global_settings` / `update_project` / `update_session` / `add_project` / `remove_project` / `add_session` / `remove_session` | admin | идемпотентное | точечные операции вместо целого AppConfig; существующий set_global_settings — алиас |
| `PickDirectory` | `app.go:1251` | нет | — | **excluded** | — | — | нативный диалог ОС; заменён аргументом `path` у `add_project`/`import_project_logs` |
| `ShowMainWindow` | `app.go:1264` | нет | — | **excluded** | — | — | оконное действие ОС: у агента нет окна |
| `MinimizeToTray` | `app.go:1275` | нет | — | **excluded** | — | — | оконное действие ОС: у агента нет окна |
| `Notify` | `app.go:1313` | нет | — | **excluded** | — | — | тост ОС для человека; агент отвечает через результат инструмента |
| `ExportLog` | `app.go:1329` | нет | MCP-09 | `export_log` | operate | создаёт файл | без нативного диалога: файл в `~/.claude-manager/exports`, в ответе путь; формат md/json/txt (store.RenderExport) |
| `CleanOldLogs` | `app.go:1367` | нет | MCP-09 | `clean_old_logs` | admin | разрушительное | удаляет данные |
| `GetSessionRoadmap` | `app.go:1401` | нет | MCP-10 | `get_session_roadmap` | read | только чтение | — |
| `GetRoadmapTaskDetail` | `app.go:1417` | нет | MCP-10 | `get_roadmap_detail` | read | только чтение | `kind=task` |
| `GetRoadmapRowDetail` | `app.go:1430` | нет | MCP-10 | `get_roadmap_detail` | read | только чтение | `kind=row` |
| `GetTopActions` | `app.go:1462` | нет | MCP-09 | `get_top_actions` | read | только чтение | — |
| `GetActionSamples` | `app.go:1472` | нет | MCP-09 | `get_action_samples` | read | только чтение | — |
| `GetDurationProfile` | `app.go:1484` | нет | MCP-09 | `get_duration_profile` | read | только чтение | — |
| `GetTokenAttribution` | `app.go:1497` | нет | MCP-09 | `get_token_attribution` | read | только чтение | — |
| `GetPermissionCandidates` | `app.go:1513` | нет | MCP-07 | `get_permission_candidates` | read | только чтение | кандидаты в постоянные правила |
| `AddPermissionRule` | `app.go:1530` | нет | MCP-07 | `add_permission_rule` | admin | аддитивное | меняет политику permissions проекта |
| `GetSkillCandidates` | `app.go:1580` | нет | MCP-11 | `get_skill_candidates` | read | только чтение | — |
| `DistillSkill` | `app.go:1610` | нет | MCP-11 | `distill_skill` | operate | аддитивное | запускает модель; создаёт только черновик |
| `GetSkills` | `app.go:1660` | нет | MCP-11 | `get_skills` | read | только чтение | — |
| `ImportProjectLogs` | `app.go:1675` | нет | MCP-09 | `import_project_logs` | operate | аддитивное | аргумент `dir` вместо диалога |
| `ApproveSkill` | `app.go:1692` | нет | MCP-11 | `approve_skill` | admin | аддитивное | пишет скилл в репозиторий |
| `ArchiveSkill` | `app.go:1714` | нет | MCP-11 | `archive_skill` | admin | идемпотентное | переносит папку скилла |
| `RestoreSkill` | `app.go:1731` | нет | MCP-11 | `restore_skill` | admin | аддитивное | пишет скилл в репозиторий |
| `GetSkillUsage` | `app.go:1750` | нет | MCP-11 | `get_skill_usage` | read | только чтение | — |
| `GetSkillAutopilot` | `app.go:1772` | нет | MCP-11 | `get_skill_autopilot` | read | только чтение | — |
| `SetSkillAutopilot` | `app.go:1808` | нет | MCP-11 | `set_skill_autopilot` | admin | идемпотентное | меняет конфиг проекта |
| `RunSkillAutopilot` | `app.go:1837` | нет | MCP-11 | `run_skill_autopilot` | operate | аддитивное | один шаг; ограничен дневным бюджетом |
| `GetSkillQuality` | `app.go:1907` | нет | MCP-11 | `get_skill_quality` | read | только чтение | — |
| `GetProjectLogFiles` | `app.go:1918` | нет | MCP-09 | `get_project_log_files` | read | только чтение | — |
| `ClearProjectLogs` | `app.go:1930` | нет | MCP-09 | `clear_project_logs` | admin | разрушительное | удаляет данные; `confirm=true` |
| `MirrorProject` | `app_mirror.go:122` | нет | MCP-08 | `mirror_project` | admin | идемпотентное | — |
| `UnmirrorProject` | `app_mirror.go:138` | нет | MCP-08 | `unmirror_project` | admin | разрушительное | — |
| `SyncProjectMirrors` | `app_mirror.go:143` | нет | MCP-08 | `sync_project_mirrors` | operate | идемпотентное | — |
| `GetProjectMirror` | `app_mirror.go:148` | нет | MCP-08 | `get_project_mirror` | read | только чтение | — |

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
| `get_metrics` | read | Без `project` — состояние rate limit (`GetRateLimitStatus`) |

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

**`set_global_settings` — устаревший алиас** (`deprecated` в описании): он шлёт частичный
объект как весь конфиг. Вместо него — `update_global_settings`; алиас удаляется отдельной
задачей после MCP-17.

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
| `get_metrics` | read | Стоимость проекта за период, без проекта — статус rate-limit (существующий) |
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
