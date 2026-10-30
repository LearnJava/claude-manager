# Slim-for-Kimi: состояние, проверка, план дальнейших работ

Ветка: **`slim-for-kimi`** (от master). Основные изменения закоммичены; история проверок — ниже.
Дата: 2026-10-30.

## Зачем

Доработки после коммита `e71344f` (2026-09-08) замедлили сессии Claude Code примерно в 5 раз
(A/B-пилот в `docs/Science_lab/` подтверждает ~2x по времени и стоимости). Решение: от текущего
HEAD вырезать эти доработки, **сохранив текущий дизайн UI и консоль**, и добавить рантайм Kimi
вместо Claude. Стратегия «HEAD минус фичи», а не откат на старый коммит (фронтенд на HEAD
несовместим с бэкендом на `e71344f` по Wails-биндингам).

## Что удалено

| Область | Состав |
|---|---|
| Experience-слой | `internal/experience`, `internal/learned`, `internal/suggest`, `internal/nightly`, `internal/guardaudit`, `cmd/guardaudit`, `cmd/cm-mcp-eval` |
| Промпт-надстройки | primer (LN-05), journal (LN-06), handoff-дистилляция (LN-15), env card, manager card, learned rules, launch tuning, `--max-turns` авто-настройка, `--exclude-dynamic-system-prompt-sections` |
| Циклы и гейты | loop detection + loop memory, strict gates, reference cases, quality labels, benchmarks, hook rules, failure rules, repo suggestions, nightly gates |
| SQLite | таблицы experience (action_signatures, skills, permission_events, failure_rules, task_labels, benchmark_*, reference_*, gate_runs, learned_sync, loop_events, ingest_state, imported_logfiles) — удалены только из кода, существующие БД не трогаем |
| MCP/control-plane | инструменты skills/failures/experience-логов (get_top_actions, get_waste_report, run_benchmark, …), их RPC, access-уровни, golden/docs |
| UI | ExperiencePanel, SkillReview, QualityPanel, LearnedFiles, HookRules, Suggestions, FailureReview, stores/experience.ts, соответствующие настройки/локали/Playwright-спеки |

Оставлено: проекты и параллельные сессии, worktree pool, очереди задач, permissions, лента логов
(LogStream feed, LiveStatus, QueuePanel/Board, RoadmapTree), история и стоимость, mixed programming,
MCP/control-plane, mirror/logship, cmdguard, рантайм Hermes.

## Что добавлено: рантайм Kimi

### Runtime и capabilities

`internal/session/runtime.go` — интерфейс `Runtime` расширен:

- `Capabilities{Resume, Effort, PermissionProtocol, Cost, TokenUsage, Images, PromptArg}` —
  вместо разбросанных `IsHermes()/IsKimi()`; менеджер и UI ориентируются на возможности, а не имена.
- `Binary`, `Env`, `ClassifyStderr`, `Preamble`, `WarnOptions` — рантайм-специфика процессов.
- Опциональные хуки: `TurnExpander` (Hermes `/skill` → `-s`), `TurnFinisher` (post-turn проверки
  Hermes: step-limit, 429-behind-401), `StderrIniter` (Kimi resume_hint из stderr).

`runOncePerTurn` (бывший `runOnceHermes`) теперь рантайм-независим: Hermes и Kimi работают через
него без изменений. Модель процессов Kimi = Hermes: **один процесс на ход, resume по id**.

### ACP wire

- `initialize` + `session/new` / `session/resume` — создание разговора и resume по id без replay.
- `session/set_mode(auto)` и `session/set_model` — прежняя автономная политика и выбранная модель.
- `session/prompt` — один ход; `agent_thought_chunk` → `thinking`, `agent_message_chunk` → `text`,
  `tool_call` / `tool_call_update` → существующие карточки инструментов.
- `session/close` — завершение live-объекта после ответа. Файлы `~/.kimi-code/sessions/` менеджер
  напрямую не читает и не изменяет.
- Известные служебные события сворачиваются в «Технические детали»; основная лента остаётся с
  рассуждениями, ответами и инструментами.

### Файлы

- `internal/session/kimi_acp.go` — per-turn JSON-RPC client ACP, resume, ошибки и маппинг событий.
- `internal/session/kimi_runtime.go` — capabilities, `ClassifyStderr`, `Preamble`, `WarnOptions`.
- `internal/session/kimi_parser.go` — прежний толерантный парсер stream-json для совместимости.
- `cmd/fakekimi` — скриптовый двойник CLI и ACP для тестов.
- `internal/session/kimi_runtime_test.go` (парсер + `buildFakekimi` + ACP),
  `internal/session/kimi_real_test.go` (реальный smoke, `CM_REAL_KIMI=1`).
- UI: `lib/models.ts` (`RUNTIME_CAPABILITIES`, `modelsFor`, `KIMI_MODELS`), Sidebar/SessionCard/
  Settings пикер и бейдж, ModelPicker (effort по capabilities), `lib/interrupt.ts` (per-turn нельзя
  прервать), SessionInput (картинки только при `Images`), SessionCard (cost → «нет данных»),
  LogEntryRow + LogStream (текст Kimi с маркером `• `, collapsible technical details).
- `config.example.toml` (`kimi_path`), `internal/config` (RuntimeKimi, KimiPath, валидация),
  `Session.Params.KimiPath` + 3 места в manager, `SetSessionRuntime` принимает `kimi`.

### Ограничения by design

- `session/set_mode(auto)` = прежняя автономная политика: Permission Queue не имитируется.
- Нет effort-флага, стоимости, usage, картинок в текущем MVP. Всё это скрыто/«нет данных», не $0.
- Менеджер владеет только своими сессиями: не подключается к сессиям Kimi Desktop; конфликт
  «in use by another Kimi Code Desktop instance» → понятное сообщение + fresh conversation
  (kind `session_not_found`). Файлы `~/.kimi-code/sessions/` напрямую не трогаем.

## Проверка

- `go build ./...`, `go vet ./...` — чисто.
- `go test ./...` — все пакеты зелёные (session, control, cm-mcp, main, store, optimization, …).
- Контрактный сьют `runtime_contract_test.go` — зелёный на **трёх** рантаймах.
- Прежний реальный smoke `TestKimiRuntime_RealKimi` через `kimi -p`: полный цикл с resume зелёный.
  Для ACP на Kimi 2.4.0 подтверждены `initialize`, `session/new`, `set_mode` и `set_model`;
  полный ход с ответом модели пока не проверен из-за повторяющихся API timeout.
- `go test ./internal/session -run TestKimi -count=1`: transient timeout → backoff → повтор того
  же prompt с тем же session id даже при `auto_restart=false`; Restart → Continue для configured
  idle-сессии вне памяти менеджера использует сохранённый id; `system.version` и
  `turn.step.retrying` отображаются понятным текстом, не сырым JSON.
- `npm run build` — зелёный; `npm run check` — 0 ошибок (остались прежние warning/hints).
- Полный `npx playwright test` до добавления Kimi-спека — **278/278** зелёные (3,5 минуты).
- `npx playwright test tests/kimi.spec.ts` — **2/2**: шесть моделей Kimi Desktop с проверенными
  CLI alias в Sidebar/Settings/ModelPicker, текст реальной wire-формы с `• `, «нет данных» вместо
  $0 и отсутствие Permission Queue.
- Изолированный Wails GUI + `fakekimi`: запуск → лента → сообщение → stop → resume; «нет данных»
  видно в карточке. Две Kimi-сессии в worktree `k1`/`k2` не смешали события; Stop не оставил
  `fakekimi`-процессов; session id и workdir восстановились после перезапуска менеджера.
- Desktop-конфликт на `fakekimi`: исходное понятное сообщение «This session is in use by another
  Kimi Code Desktop instance.» дошло до ленты. После отдельного указания Claude/Hermes не
  запускались; их ранее зелёные сьюты не повторялись.

## Что осталось сделать

### Обязательное для «классно работает»

1. **Сравнение с обычными окнами Kimi**: время до результата, число вмешательств человека,
   зависшие/потерянные сессии — не только токены.

### По желанию / следующие этапы

- Вычистить инертные поля удалённых фич из `internal/config` (ExperienceTracking, LoopDetection,
  EnvCard, ManagerCard, ContextPrimer, HandoffContextTokens, GateRounds, AutoSkills, Journal,
  GateSchedule*, MaxTurns*, ExcludeDynamic, Waste*) + `config.example.toml` + `config.local` overlay.
- Полная синхронизация `docs/architecture.md` и `docs/design-decisions.md` (сейчас аддендумы).
- Полноценный интерактивный `session/request_permission` / `elicitation` вместо текущего `auto`.
- Каталог slash-команд Kimi для автокомплита (сейчас честно отключён).
- `envcheck`: детект бинаря `kimi` и версии в Settings → Environment.
- Опциональный e2e `CM_REAL_KIMI=1` сценарий Desktop-конфликта.

## Команды

```bash
go build ./... && go vet ./... && go test ./...
CM_REAL_KIMI=1 go test ./internal/session/ -run TestKimiRuntime_RealKimi -v
cd frontend && npm run build && npx playwright test
```
