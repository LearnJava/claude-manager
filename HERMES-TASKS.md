# HERMES-TASKS — Hermes CLI как runtime сессий (экспериментальная ветка)

Ветка: `exp/hermes-runtime`. Менеджер запускает **`hermes` CLI** (`hermes chat`) вместо
`claude` и работает с его выводом `--format stream-json`. ACP/TUI-протоколы не используются.

Источники: `website/docs/reference/cli-commands.md` (разделы `hermes chat`, `--format
stream-json`, Global options), `user-guide/security.md` (approvals), `user-guide/features/hooks.md`,
`hermes chat --help` и `hermes_cli/stream_json.py` установленного **Hermes Agent v0.21.4**.
Всё, что помечено «проверено», запускалось на этой машине 2026-09-24.

## Инварианты

1. **`claude` остаётся runtime по умолчанию.** Hermes включается для отдельной сессии полем
   `runtime = "hermes"`. Если поля нет, аргументы, промпты и события не меняются ни на байт.
2. **Один контракт событий.** UI, SQLite, experience layer и control-plane не знают, какой
   runtime работает. Всё, что относится к Hermes, живёт в адаптере.
3. **Деньги видны до запуска.** Claude-модели через Hermes по OAuth работают только на Max и
   списываются как *extra usage* (`integrations/providers` → Anthropic). В UI при выборе такой
   связки показывается предупреждение.
4. **В проект пишем только после одобрения в UI** (навыки, которые Hermes создал сам, в том
   числе).
5. **Тесты без токенов:** двойник `fakehermes`.

## Ключи `hermes chat`, которые мы используем

| Наше поле / нужда | Ключ Hermes | Примечание |
|---|---|---|
| Промпт (любой текст) | `--query-file -` + промпт в stdin | **Проверено:** кавычки, `$(…)` и обратные кавычки доходят как есть. `-q` в аргументах опасен на Windows (длина и экранирование) |
| Машинный вывод | `--format stream-json` | Включает `--quiet`, требует query |
| Продолжить разговор | `--resume <session_id>` (`-r`) | **Проверено:** второй процесс помнит первый ход, `session_id` тот же |
| Model | `-m`, `--model` | |
| Провайдер | `--provider` | Новое поле `hermes_provider` |
| Effort | `--reasoning` | `none…max`, наши `low/medium/high` подходят напрямую |
| `max_turns` | `--max-turns` | Считаются итерации инструментов **на ход** |
| `permission_mode = bypassPermissions` | `--yolo` | Остальные режимы: `approvals.single_query_mode` (по умолчанию `deny`, опасная команда блокируется, агент ищет обход) |
| `use_worktree` | `--worktree` | Для developer-сессий остаётся `false` (протокол проекта) |
| Каталог проекта | `cmd.Dir` + `--in <dir>` | `--in` ограничивает поиск `--resume latest` этим каталогом |
| Скрыть из списков Hermes | `--source tool` | **Проверено** |
| Профиль (память и навыки) | `-p <profile>` (глобальный ключ, **до** `chat`) | HR-12 |
| Хуки без TTY | `--accept-hooks` | Без него непроверенные хуки молча пропускаются |
| Откат | `--checkpoints` | Затем `hermes checkpoints` / `/rollback` |
| Картинка | `--image <path>` | Только одна на ход: base64 → временный файл |
| Предзагрузка навыка | `-s <skill>` | Так передаём протокол ask-user и предупреждение о фоне (флага system-prompt-append нет) |
| Лимит по времени | `--run-budget <sec>` | Аналога `--max-budget-usd` нет, деньги проверяем после хода (HR-06) |

`--session-id`, `--append-system-prompt`, `--allowed-tools`/`--disallowed-tools`, `--add-dir`,
`--fallback-model`, `--max-budget-usd` у `hermes chat` **нет**. Замены указаны в задачах ниже.

## Поток `--format stream-json` (проверено)

```
{"type":"system","subtype":"init","model":…,"session_id":"20260924_111123_993e42"}
{"type":"text","text":"…"}                      ← дельты, склеивать
{"type":"tool_use","name":"terminal","input":{…},"tool_call_id"?}
{"type":"tool_result","name":…,"output":"…(≤5000)","duration_ms":…,"is_error":false}
{"type":"result","session_id":…,"exit_code":0,"text":"…",
 "tokens":{"input","output","total","cache_read","cache_write"},"duration_ms":…,"error"?}
```
`result` всегда последний, `exit_code` совпадает с кодом процесса (`0` успех, `1` ошибка,
частичное выполнение или исчерпан бюджет, `130` прерывание). Строка `session_id:` уходит в stderr.
Стоимости в потоке нет, но она есть в `%LOCALAPPDATA%\hermes\state.db` → `sessions.
estimated_cost_usd` (**проверено**: `0.0221` за два хода, значение накопительное по сессии).

## Главное отличие от `claude`: один процесс = один ход

`claude` держит процесс открытым и принимает ходы через stdin. `hermes chat --format
stream-json` отвечает на **один** запрос и завершается. Следствия:

| Сценарий | Как работает |
|---|---|
| Автономная сессия (task_source/auto_restart) | Ложится идеально: у нас и так одна задача = один процесс. Закрывать stdin по `result` не нужно, процесс выходит сам |
| Интерактивная сессия | Каждое `SendMessage` запускает новый процесс с `--resume <id>`. Между ходами процесса нет (статус «ожидает ввода»), ресурсы не расходуются |
| Живая смена модели | Бесплатна: следующий ход идёт с новым `-m`. Soft-restart не нужен |
| Ask-user (вопрос в автономном режиме) | Процесс уже вышел. Ответ человека (или автоответ по таймауту) = следующий ход с `--resume` |
| Stop | Kill процесса. `session_id` сохранён, можно продолжить |
| Crash recovery | `session_id` из `init` пишется в state-файл, как сейчас. Продолжение = `--resume` |
| Разрешения | Запросов разрешения в потоке **нет** (одиночный запрос не ждёт человека). См. HR-07 |

## Фаза 1: сессии работают на `hermes`

### HR-01. Интерфейс runtime в `internal/session` — ✓ сделано упрощённо
**Сделано:** вместо интерфейса — развилка в `runOnce` (`Config.IsHermes()` → `runOnceHermes`,
`internal/session/hermes_runtime.go`) и общий `handleEvent(ParsedEvent)`, выделенный из
`handleLine`. Путь `claude` не тронут: все прежние тесты зелёные. Полноценный интерфейс — если
появится третий runtime.

Выделить из `Session.runOnce`/`buildCLIArgs`/`handleLine` зависимость от CLI:
```go
type Runtime interface {
    Args(p LaunchParams) []string                 // что запускать
    Stdin(p LaunchParams, prompt Prompt) []byte   // claude: stream-json envelope; hermes: сырой текст
    Parse(line string) []ParsedEvent             // в общий тип parser.go
    PerTurnProcess() bool                        // hermes: true
}
```
`claudeRuntime` — текущий код без изменений поведения.
**Готово когда:** существующие тесты `internal/session` и e2e зелёные без правок сценариев,
golden-тест на аргументы `claude` не меняется.

### HR-02. Конфиг — ✓ сделано (без PLAN.md)
`SessionConfig.Runtime` (`runtime`: `claude|hermes`, по умолчанию `claude`), `HermesProvider`,
`HermesProfile`, `HermesSkills []string`; `GlobalSettings.HermesPath` (`hermes_path`, по умолчанию
`hermes`). Doc-sync: `config.example.toml`, PLAN.md, CLAUDE.md.

### HR-03. `fakehermes` — ✓ сделано минимально
**Сделано:** `cmd/fakehermes` без файлов сценариев: эхо первой строки запроса, пара
tool_use/tool_result, дельты текста; `FAIL_429` в запросе → упавший `result`;
`FAKEHERMES_LOG` пишет argv+запрос каждого вызова. Сценарные файлы — по мере надобности.

`cmd/fakehermes` принимает те же ключи (`chat --query-file - --format stream-json --resume …`) и
выдаёт поток Hermes по сценариям `testdata/hermes-scenarios/*.json`. Сценарии: happy, tool
error, `exit_code:1` с `error`, прерывание 130, resume (помнит предыдущий промпт), todo, ask-user
маркер, 429-ошибка. Conformance-тест: реальный вывод, записанный 2026-09-24, кладётся в
`testdata/hermes-stream/` и прогоняется через тот же парсер.

### HR-04. `hermesRuntime`: запуск и парсер — ✓ сделано (без e2e через control-plane)
**Сделано:** `hermes_parser.go` + `hermes_runtime.go`. Тесты: парсер на трёх реальных записях,
аргументы, цикл автономной сессии, интерактив с `--resume` и сменой модели между ходами,
429 → rate limit (`hermes_test.go`, `hermes_runtime_test.go` на fakehermes), и прогон с
настоящим `hermes` (`hermes_real_test.go`, `CM_REAL_HERMES=1`: два хода, второй помнит первый).
Пока ask-user и предупреждение о фоне идут преамбулой к первому ходу (HR-05 заменит на навык).

- Аргументы из таблицы ключей, промпт в stdin.
- Парсер: `system/init` → `EvtInit` (session_id = `CLISessionID`); `text` склеивается и
  сбрасывается в assistant-запись на `tool_use`/`result`; `tool_use`/`tool_result` сопоставляются
  по `tool_call_id`, иначе FIFO по имени; `result` → `EvtResult` (tokens: input/output/cache_read/
  cache_write → наши поля; `exit_code≠0` или `error` → ошибка хода).
- `todo` / `todo_list` `tool_use` (`input.todos[{id,content,status}]`) → `updateTodos`
  (**проверено**: в этом профиле инструмент называется `todo_list`, в стандартном Hermes —
  `todo`, поддерживаем оба).
- Интерактивный режим: процесс на ход, `--resume`. Автономный: как сейчас.
- stderr: строку `session_id:` пропускать, остальное как у claude (403 и т.п.).
**Готово когда:** e2e `hermes-happy`, `hermes-interactive-resume`, `hermes-autonomous-loop`,
`hermes-crash-resume` на `fakehermes` проходят, а в UI сессия выглядит так же, как claude-сессия.

### HR-05. Протокол ask-user и предупреждение о фоне через навык
Флага `--append-system-prompt` нет. Текст `askUserProtocolPrompt` и
`backgroundTaskWarningPrompt` ставится навыком `cm-autonomous` в `<HERMES_HOME>/skills/`
(ставит менеджер, только свой собственный каталог) и подключается ключом `-s cm-autonomous`
только для автономных запусков. Системный промпт не меняется, поэтому кэш промпта не
сбрасывается.

### HR-06. Метрики и стоимость
Токены берутся из `result.tokens`. Стоимость: read-only `state.db` → `sessions.
estimated_cost_usd` по `session_id`, ход = разница с предыдущим значением. `cost_status`/
`cost_source` сохраняются как признак оценки. Путь к БД: `HERMES_HOME` профиля, по умолчанию
`%LOCALAPPDATA%\hermes\state.db`, переопределяется env (для тестов). Колонка
`session_runs.runtime` (additive ALTER). `max_budget_usd` проверяется после каждого хода: если
превышен, сессия останавливается.

### HR-07. Разрешения
В одиночном запросе Hermes не спрашивает человека. Этап 1: `bypassPermissions` → `--yolo`,
остальные режимы → `approvals.single_query_mode: deny` (опасное блокируется, в лог уходит
запись). Настоящие интерактивные разрешения — HR-09.

### HR-08. UI — ◐ частично
**Сделано:** Settings — путь к Hermes, в сессии селект Runtime, провайдер, профиль, свободное
поле модели и пояснение, что не применяется; в Sidebar вместо выпадающего списка Claude-моделей
метка ☤ с моделью. Нет: кнопки «Проверить», навыков в UI, скрытия PermissionBanner.

Settings → сессия: селект Runtime, поля провайдер/профиль/навыки, баннер про биллинг
(инвариант 3). Кнопка «Проверить» запускает `hermes --version`. В `lib/models.ts` появляются
модели не от Anthropic, `normalizeModel` пропускает `provider/model`. Для hermes-сессии
PermissionBanner скрыт до HR-09.

**Аналитики** (preflight, journal, handoff, skill, brief, roadmap) на фазе 1 остаются на
`claude --json-schema`: у Hermes схемы нет. Фаза 2: `hermes -z … --usage-file` + схема в промпте
+ валидация, если нужен запуск без `claude`.

## Фаза 2: то, ради чего это затевается

### HR-09. Живые разрешения через хук `pre_tool_call` → наш UI
`cmd/cm-hook` — shell-хук Hermes (JSON в stdin: `tool_name`, `tool_input`, `session_id`, `cwd`).
Сначала применяет `PermissionRule` сессии. Если ни одно правило не подошло, обращается к
control-plane менеджера (`/permission`, loopback + токен из `control.json`) и **ждёт ответа
человека** из PermissionBanner. Ответ «deny» → `exit 2` / `{"decision":"block","reason":…}`.
Так в CLI-режиме возвращаются интерактивные разрешения, причём правила применяются **до**
вызова. Установка: блок `hooks:` в профиле + `--accept-hooks`.

### HR-10. Обязательные гейты через `pre_verify`
Тот же `cm-hook` на `pre_verify` (агент правил код и собирается закончить) запускает
`project.gates`. Если гейт красный, возвращает `{"action":"continue","message":<вывод>}`; после
3-й попытки (`extra.attempt`) — `needs_human`. Закончить ход с красным билдом нельзя.

### HR-11. Самообучение под контролем
- `skills.external_dirs` → `<project>/.claude/skills`: Hermes видит утверждённые навыки проекта.
- Навыки, которые Hermes написал сам (`<HERMES_HOME>/skills/**`, по mtime), появляются в
  Skills-табе как draft с источником `hermes`. Утверждение через `WriteSkillFile` (LN-10), замер
  эффекта через LN-11.
- `hermes curator run --dry-run` выводится как список предложений, без автоприменения.

### HR-12. Профиль Hermes на проект
`-p <project>`: отдельные память, навыки, хуки и `state.db` на проект. Знания Lumen не
протекают в claude-manager, а хуки/навыки из HR-05/09/10 ставятся в изолированный профиль, а не
в личный.

### HR-13. Цепочка провайдеров вместо паузы на rate limit
`fallback_providers` + credential pool (`least_used`) в профиле. Hermes переключается сам, а
менеджер лишь показывает событие. Пауза rate-limit в менеджере остаётся только на случай, когда
`result.error` говорит, что упала вся цепочка.

### HR-14. Бенчмарк (без него в master не мержим)
Одни и те же задачи на `claude/sonnet`, `hermes + claude-sonnet`, `hermes + дешёвая модель`.
Сравниваем completed rate, input tokens, $/задача, turns, время, холодный старт процесса
(у `hermes` Python-лаунчер, а в интерактивном режиме процесс запускается на каждый ход).
Отчёт в `docs/hermes-benchmark.md`. Успех: у дешёвой модели $/задача ≤ 1/3, а completed rate не
ниже 80% от claude.

### HR-15. `state.db` как источник для experience layer
`internal/experience/hermesdb.go`: `messages` → `Trajectory` (как LN-17). Actions/Timing/Cost by
tool/candidate mining работают и для hermes-сессий. `hermes approvals suggest --json` становится
вторым источником для вкладки Permissions.

### HR-16. Mixed programming v2
Воркер = hermes-сессия на модели воркера, в worktree, с инструментами и гейтами HR-10 вместо
FIND/REPLACE. Сравнение со старым режимом на тех же брифах (`quality.go`).

## Фаза 3: исследование
- **HR-17.** Kanban Hermes vs `STATUS-P1.md` для multi-developer.
- **HR-18.** `--checkpoints` + кнопка «откатить ход».
- **HR-19.** Computer Use Hermes для GUI-тестов самого менеджера (GUI-TESTS.md).

## Риски

| Риск | Что делаем |
|---|---|
| Процесс на каждый ход (интерактив) медленнее | Замер в HR-14. Если плохо, для интерактивных сессий позже можно перейти на ACP |
| Нет интерактивных разрешений в CLI | HR-07 (deny/yolo) → HR-09 (хук + UI) |
| Формат stream-json Hermes меняется | Conformance-тест на реальных записях, проверка `hermes --version` при старте |
| Стоимость не в потоке | `state.db`, иначе помечаем стоимость как оценку |
| Биллинг Claude через Hermes | Инвариант 3 |

## Порядок
HR-01 → HR-02 → HR-03 → HR-04 → HR-06 → HR-05 → HR-07 → HR-08 → **HR-14 (первый замер)** →
HR-12 → HR-09 → HR-10 → HR-11 → HR-13 → HR-15 → HR-16 → фаза 3.
