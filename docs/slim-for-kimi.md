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

### Реальный wire Kimi 2.4.0 (проверено пробами)

- `{"role":"meta","type":"system.version"}` — баннер, системная запись в лог.
- `{"role":"assistant","content":"..."}` — текст агента.
- `{"role":"assistant","tool_calls":[{"type":"function","id":"tool_...",
  "function":{"name":"Read","arguments":"{...}"}}]}` — вызов инструмента (name/arguments вложены
  в `function`).
- `{"role":"tool","tool_call_id":"...","content":"..."}` — результат.
- `{"role":"meta","type":"session.resume_hint","session_id":"session_..."}` — id разговора,
  мапится в `EventInit` (бывает и в stderr — обрабатывается через `StderrIniter` + `wireInitID`).
- **Нет init и result событий.** `kimiStream.Flush` синтезирует `EventResult` (success/error),
  чтобы per-turn цикл узнал окончание хода. Токенов и стоимости нет — UI показывает «нет данных».

### Файлы

- `internal/session/kimi_runtime.go` — `buildKimiArgs` (`-p <prompt> --output-format stream-json
  [--session id] [-m model]`), capabilities, `ClassifyStderr` (429/403/session-not-found/Desktop
  conflict), `Preamble`, `WarnOptions` (effort/permission_mode/budget/fallback/tools/add_dirs).
- `internal/session/kimi_parser.go` — толерантный парсер stream-json → `ParsedEvent`.
- `cmd/fakekimi` — скриптовый двойник CLI для тестов.
- `internal/session/kimi_runtime_test.go` (парсер + `buildFakekimi`),
  `internal/session/kimi_real_test.go` (реальный smoke, `CM_REAL_KIMI=1`).
- UI: `lib/models.ts` (`RUNTIME_CAPABILITIES`, `modelsFor`, `KIMI_MODELS`), Sidebar/SessionCard/
  Settings пикер и бейдж, ModelPicker (effort по capabilities), `lib/interrupt.ts` (per-turn нельзя
  прервать), SessionInput (картинки только при `Images`), SessionCard (cost → «нет данных»),
  LogEntryRow + LogStream (текст Kimi с маркером `• `).
- `config.example.toml` (`kimi_path`), `internal/config` (RuntimeKimi, KimiPath, валидация),
  `Session.Params.KimiPath` + 3 места в manager, `SetSessionRuntime` принимает `kimi`.

### Ограничения by design

- `kimi -p` = политика `auto`: **никаких вопросов разрешений** (сессии полностью автономные).
  Permission Queue для Kimi не имитируется.
- Нет effort-флага, стоимости, usage, картинок. Всё это скрыто/«нет данных», не $0.
- Промпт идёт аргументом `-p`: лимит командной строки Windows (~32К). Длинные автономные
  промпты (preamble + task) пока принимаем как есть; если упёрлись — смотреть ACP или укорачивать.
- Менеджер владеет только своими сессиями: не подключается к сессиям Kimi Desktop; конфликт
  «in use by another Kimi Code Desktop instance» → понятное сообщение + fresh conversation
  (kind `session_not_found`). Файлы `~/.kimi-code/sessions/` напрямую не трогаем.

## Проверка (всё зелёное)

- `go build ./...`, `go vet ./...` — чисто.
- `go test ./...` — все пакеты зелёные (session, control, cm-mcp, main, store, optimization, …).
- Контрактный сьют `runtime_contract_test.go` — зелёный на **трёх** рантаймах.
- **Реальный smoke** `TestKimiRuntime_RealKimi` (Kimi 2.4.0): запуск → поток событий → второе
  сообщение → stop → resume по session id; второй ход и resume помнят первый, процессы не остаются.
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
- `kimi acp` для полноценного интерактивного режима: prompt/cancel/resume/model switching и
  запросы разрешений ([ACP docs](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-acp.html)).
- Каталог slash-команд Kimi для автокомплита (сейчас честно отключён).
- `envcheck`: детект бинаря `kimi` и версии в Settings → Environment.
- Опциональный e2e `CM_REAL_KIMI=1` сценарий Desktop-конфликта.

## Команды

```bash
go build ./... && go vet ./... && go test ./...
CM_REAL_KIMI=1 go test ./internal/session/ -run TestKimiRuntime_RealKimi -v
cd frontend && npm run build && npx playwright test
```
