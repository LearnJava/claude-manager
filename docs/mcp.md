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
`CM_MCP_LEVEL`); реализация — MCP-05. Инструменты выше уровня **не попадают
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
несёт его), чтобы урезанный `cm-mcp` нельзя было обойти прямым вызовом
`/rpc`. Тестовая обвязка GUI (`docs/testing-harness.md`) запускает `cm-mcp` с
`CM_MCP_LEVEL=admin` — так остаются рабочими существующие сценарии
(`approve_permission` в Playwright-тестах).

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
| `GetSessionState` | `app.go:808` | да | MCP-06 | `get_session_state` | read | только чтение | сохранённое состояние для resume |
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
| `SetSessionRuntime` | `app.go:1002` | нет | MCP-06 | `set_session_runtime` | operate | идемпотентное | claude / hermes |
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
| `ExportLog` | `app.go:1329` | нет | MCP-09 | `export_log` | read | только чтение | без нативного диалога: текст в ответе, формат md/json/txt (store.RenderExport) |
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
