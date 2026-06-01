# Claude Session Manager — план реализации

Десктопное приложение для управления сессиями Claude Code.
Запускает, мониторит и останавливает параллельные сессии `claude` CLI для любых проектов.

---

## 1. Назначение

Замена ручного запуска `claude -p` и скрипта `orchestrator.py`.
Один GUI для всех проектов, любого количества сессий, с реалтайм-логами и контролем.

**Что это НЕ:**
- Не IDE и не редактор кода
- Не обёртка над API Anthropic — работает только через CLI `claude`
- Не часть какого-либо проекта — standalone утилита

---

## 2. Стек

| Компонент | Технология | Почему |
|---|---|---|
| Backend | **Go 1.23+** | Горутины для параллельных процессов, `os/exec` для управления CLI, каналы для стриминга логов |
| Desktop shell | **Wails v2** | Один `.exe` без зависимостей, WebView2 (встроен в Windows 10/11), кросс-платформа |
| Frontend | **Svelte** (или vanilla TS) | Лёгкий, быстрый, хорошая интеграция с Wails bindings |
| CSS | **Tailwind CSS** | Быстрая вёрстка дашборда |
| Конфиг | **TOML** | Человекочитаемый, стандарт для Go/Rust проектов |
| Хранение логов | **SQLite** | История сессий, метрики, поиск по логам |
| IPC (Go ↔ Frontend) | **Wails bindings** | Go-функции вызываются из JS как обычные async-функции, события через runtime.EventsEmit |

### Зависимости Go

```
github.com/wailsapp/wails/v2    — desktop shell
github.com/BurntSushi/toml       — конфиг
github.com/mattn/go-sqlite3      — история/логи
```

Минимум зависимостей. Никаких фреймворков для "DI", "ORM" и прочего.

---

## 3. Архитектура

```
claude-manager/
├── main.go                     # Wails bootstrap
├── app.go                      # App struct — Wails lifecycle (OnStartup, OnShutdown)
├── go.mod
├── go.sum
│
├── internal/
│   ├── config/
│   │   ├── config.go           # Загрузка/сохранение TOML конфига
│   │   └── types.go            # Структуры: AppConfig, ProjectConfig, SessionConfig
│   │
│   ├── session/
│   │   ├── manager.go          # SessionManager: запуск/остановка/список сессий
│   │   ├── session.go          # Session: один процесс claude, стриминг stdout/stdin
│   │   ├── parser.go           # Парсинг stream-json вывода Claude CLI
│   │   ├── input.go            # Запись в stdin: user messages, permission responses
│   │   └── ratelimit.go        # Детекция rate limit, retry-логика, таймеры
│   │
│   ├── permission/
│   │   ├── handler.go          # Обработка permission requests от Claude CLI
│   │   ├── rules.go            # Матчинг правил авто-одобрения (TOML + runtime)
│   │   └── queue.go            # Очередь ожидающих разрешений (для UI)
│   │
│   ├── analysis/
│   │   ├── preflight.go        # Pre-flight analysis: запуск analyst-сессии
│   │   ├── plan.go             # TaskPlan: хранение, execution order, зависимости
│   │   └── schema.go           # JSON Schema для структурированного ответа аналитика
│   │
│   ├── optimization/
│   │   ├── context.go          # Мониторинг контекста, авто-рестарт по порогу
│   │   ├── cache.go            # Cache efficiency, warming strategy
│   │   ├── loop.go             # Детекция зацикливания tool calls
│   │   ├── routing.go          # Auto model routing по сложности задачи
│   │   └── reporter.go         # Сводный отчёт по экономии
│   │
│   ├── project/
│   │   └── project.go          # Project: группа сессий, привязка к директории
│   │
│   ├── store/
│   │   ├── store.go            # SQLite: запись/чтение истории сессий
│   │   └── migrations.go       # Схема БД
│   │
│   └── hooks/
│       └── hooks.go            # Pre/post хуки: скрипты до/после задачи
│
├── frontend/
│   ├── index.html
│   ├── src/
│   │   ├── main.ts             # Точка входа Svelte
│   │   ├── App.svelte          # Корневой layout
│   │   ├── stores/
│   │   │   ├── sessions.ts     # Svelte store: состояние сессий
│   │   │   └── projects.ts     # Svelte store: проекты
│   │   ├── components/
│   │   │   ├── Sidebar.svelte          # Дерево проектов и сессий
│   │   │   ├── LogStream.svelte        # Реалтайм-лог одной сессии
│   │   │   ├── SessionInput.svelte     # Поле ввода для отправки сообщений в сессию
│   │   │   ├── SessionCard.svelte      # Карточка сессии: статус, метрики, кнопки
│   │   │   ├── ProjectSettings.svelte  # Настройки проекта (модалка)
│   │   │   ├── SessionSettings.svelte  # Настройки сессии (модалка)
│   │   │   ├── PermissionBanner.svelte # Баннер запроса разрешения поверх лога
│   │   │   ├── PermissionQueue.svelte  # Глобальная очередь разрешений (все сессии)
│   │   │   ├── PlanReview.svelte       # Экран просмотра/редактирования плана задачи
│   │   │   ├── StatusBar.svelte        # Нижняя панель: сводка
│   │   │   ├── History.svelte          # История завершённых задач
│   │   │   └── RateLimitBanner.svelte  # Баннер rate limit с таймером
│   │   └── lib/
│   │       ├── wailsBindings.ts        # Автогенерация Wails
│   │       └── formatters.ts           # Форматирование логов, времени
│   ├── wailsjs/                # Автогенерация Wails (Go→JS биндинги)
│   └── package.json
│
├── config.example.toml         # Пример конфига
└── PLAN.md                     # Этот файл
```

### Потоки данных

```
┌─────────────────────────────────────────────────────────┐
│                      Go Backend                         │
│                                                         │
│  ┌──────────┐    ┌───────────────┐    ┌──────────────┐  │
│  │ Config   │───▶│ SessionManager│───▶│ SQLite Store │  │
│  │ (TOML)   │    │               │    │ (история)    │  │
│  └──────────┘    │  ┌─────────┐  │    └──────────────┘  │
│                  │  │Session 1│──┼──EventsEmit──┐       │
│                  │  │(goroutine) │              │       │
│                  │  ├─────────┤  │              │       │
│                  │  │Session 2│──┼──EventsEmit──┤       │
│                  │  │(goroutine) │              │       │
│                  │  ├─────────┤  │              ▼       │
│                  │  │Session N│──┼───▶ ┌─────────────┐  │
│                  │  └─────────┘  │    │  Frontend    │  │
│                  └───────────────┘    │  (Svelte)    │  │
│         ▲                            │             │  │
│         │  Wails bindings (JS→Go)    │  WebView2   │  │
│         └────────────────────────────│             │  │
│                                      └─────────────┘  │
└─────────────────────────────────────────────────────────┘
```

1. **Config** загружается при старте, определяет проекты и сессии
2. **SessionManager** владеет всеми Session-горутинами
3. Каждая **Session** — отдельная горутина, запускает `claude -p`, читает stdout построчно
4. Каждая строка парсится через **parser.go** (stream-json) и отправляется во фронт через `runtime.EventsEmit`
5. Frontend получает события и обновляет UI в реальном времени
6. Управляющие команды (start/stop/pause) идут обратно через Wails bindings (JS вызывает Go-функции)
7. **Store** пишет завершённые сессии в SQLite для истории

---

## 4. Конфиг (TOML)

Файл: `~/.claude-manager/config.toml` (или рядом с бинарником).

```toml
# Глобальные настройки
[settings]
claude_path = "claude"                    # Путь к CLI (если не в PATH)
default_retry_delay = 30                  # Секунд между retry при ошибке
rate_limit_pause = 300                    # Секунд паузы при rate limit
log_retention_days = 30                   # Сколько хранить историю в SQLite
theme = "dark"                            # dark | light

# Pre-flight analysis (см. секцию 17)
preflight_analysis = true                 # Включить анализ перед запуском
preflight_model = "haiku"                 # Модель для анализа
preflight_max_budget = 0                  # 0 = без лимита. >0 = лимит в USD
preflight_auto_approve_single = true      # Если single_session → запускать сразу

# Permission handling (см. секцию 16)
permission_notify_after = 30              # Секунд до усиленного уведомления
permission_timeout = 0                    # 0 = ждать бесконечно
permission_timeout_action = "deny"        # deny | pause_session
permission_native_notification = true     # Windows toast когда свёрнуто
permission_sound = true                   # Звуковой сигнал

# Budget alerts (см. секцию 18)
daily_budget_alert = 0                    # 0 = выкл. >0 = уведомление при превышении $/день
weekly_budget_alert = 0                   # 0 = выкл. >0 = уведомление при превышении $/неделя
rate_limit_alert_threshold = 0.80         # Уведомление при утилизации rate limit > 80%

# ─── Проект 1 ───────────────────────────────────────────

[[project]]
name = "lumen-browser"
path = 'D:\RustProjects\lumen-browser'

  [[project.session]]
  name = "P1"
  prompt = """
  Ты разработчик P1. Прочитай STATUS-P1.md.
  Если есть 'In progress' — продолжи. Если нет — возьми первую из 'Next'.
  Когда задача завершена — вызови /lumen-task-finish.
  """
  auto_restart = true                     # Перезапуск после завершения задачи
  max_tasks = 0                           # 0 = без лимита
  stop_when_no_tasks = true               # Остановка если нет задач
  task_source = "STATUS-P1.md"            # Файл для проверки наличия задач (опц.)

  # Модель и производительность
  model = "sonnet"                        # Модель: opus | sonnet | haiku | полное имя
  fallback_model = ""                     # Fallback при перегрузке (опц.)
  effort = "high"                         # low | medium | high | xhigh | max

  # Разрешения (см. секцию 16)
  permission_mode = "acceptEdits"         # default | acceptEdits | auto | plan | dontAsk | bypassPermissions
  allowed_tools = ["Bash(cargo *)", "Bash(npm *)"]  # Белый список (опц.)
  disallowed_tools = ["Bash(rm *)"]       # Чёрный список (опц.)

  # Бюджет
  max_budget_usd = 0                      # 0 = без лимита. >0 = лимит в USD

  # Worktree
  use_worktree = true                     # Claude создаёт git worktree (--worktree)

  # Контекст
  system_prompt_append = ""               # Добавить к системному промпту (опц.)
  add_dirs = []                           # Доп. директории для доступа (опц.)

  # Pre-flight (override глобальной настройки)
  preflight = "auto"                      # always | auto | never

  # Правила авто-одобрения разрешений
  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "cargo *"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Edit"
  pattern = "*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "rm *"
  decision = "deny"

  [[project.session]]
  name = "P2"
  prompt = """
  Ты разработчик P2. Прочитай STATUS-P2.md. ...
  """
  auto_restart = true
  dangerously_skip_permissions = true

  [[project.session]]
  name = "P3"
  prompt = "Ты разработчик P3. ..."
  auto_restart = true
  dangerously_skip_permissions = true

  [[project.session]]
  name = "P4"
  prompt = "Ты разработчик P4. ..."
  auto_restart = true
  dangerously_skip_permissions = true

# ─── Проект 2 ───────────────────────────────────────────

[[project]]
name = "my-api"
path = 'D:\Projects\my-api'

  [[project.session]]
  name = "backend"
  prompt = "Work on the next backend task from TODO.md."
  auto_restart = false
  dangerously_skip_permissions = false    # Ручное подтверждение

  [[project.session]]
  name = "frontend"
  prompt = "Work on the next frontend task."
  auto_restart = false
```

### Переменные в промпте

Промпт поддерживает подстановку:
- `{name}` — имя сессии
- `{project}` — имя проекта
- `{path}` — путь к проекту
- `{task_source}` — содержимое task_source файла (если указан)

---

## 5. Модели данных (Go)

### Config

```go
type AppConfig struct {
    Settings GlobalSettings   `toml:"settings"`
    Projects []ProjectConfig  `toml:"project"`
}

type GlobalSettings struct {
    ClaudePath        string `toml:"claude_path"`
    DefaultRetryDelay int    `toml:"default_retry_delay"`
    RateLimitPause    int    `toml:"rate_limit_pause"`
    LogRetentionDays  int    `toml:"log_retention_days"`
    Theme             string `toml:"theme"`

    // Pre-flight analysis
    PreflightAnalysis         bool    `toml:"preflight_analysis"`
    PreflightModel            string  `toml:"preflight_model"`
    PreflightMaxBudget        float64 `toml:"preflight_max_budget"`        // 0 = без лимита
    PreflightAutoApproveSingle bool   `toml:"preflight_auto_approve_single"`

    // Permission handling
    PermissionNotifyAfter      int    `toml:"permission_notify_after"`
    PermissionTimeout          int    `toml:"permission_timeout"`          // 0 = бесконечно
    PermissionTimeoutAction    string `toml:"permission_timeout_action"`
    PermissionNativeNotification bool `toml:"permission_native_notification"`
    PermissionSound            bool   `toml:"permission_sound"`
}

type ProjectConfig struct {
    Name     string          `toml:"name"`
    Path     string          `toml:"path"`
    Sessions []SessionConfig `toml:"session"`
}

type SessionConfig struct {
    Name                string           `toml:"name"`
    Prompt              string           `toml:"prompt"`
    AutoRestart         bool             `toml:"auto_restart"`
    MaxTasks            int              `toml:"max_tasks"`
    StopWhenNoTasks     bool             `toml:"stop_when_no_tasks"`
    TaskSource          string           `toml:"task_source"`

    // Модель и производительность
    Model               string           `toml:"model"`
    FallbackModel       string           `toml:"fallback_model"`
    Effort              string           `toml:"effort"`

    // Разрешения
    PermissionMode      string           `toml:"permission_mode"`
    AllowedTools        []string         `toml:"allowed_tools"`
    DisallowedTools     []string         `toml:"disallowed_tools"`
    PermissionRules     []PermissionRule `toml:"permission_rule"`

    // Бюджет
    MaxBudgetUSD        float64          `toml:"max_budget_usd"`     // 0 = без лимита

    // Worktree
    UseWorktree         bool             `toml:"use_worktree"`

    // Контекст
    SystemPromptAppend  string           `toml:"system_prompt_append"`
    AddDirs             []string         `toml:"add_dirs"`

    // Pre-flight
    Preflight           string           `toml:"preflight"`          // always | auto | never

    // Хуки
    PreTaskHook         string           `toml:"pre_task_hook"`
    PostTaskHook        string           `toml:"post_task_hook"`
}

type PermissionRule struct {
    Tool     string `toml:"tool"`       // "Bash", "Edit", "Write", "*"
    Pattern  string `toml:"pattern"`    // glob для команды или пути
    Decision string `toml:"decision"`   // "allow", "deny", "ask"
}
```

### Session state

```go
type SessionStatus int

const (
    StatusIdle              SessionStatus = iota  // Не запущена
    StatusStarting                                // Запускается
    StatusAnalyzing                               // Pre-flight analysis запущен
    StatusWorking                                 // Claude работает
    StatusWaitingPermission                       // Ждёт ответа на permission request
    StatusRateLimited                             // Пауза по rate limit
    StatusRetrying                                // Пауза перед retry (ошибка)
    StatusStopping                                // Ждёт завершения текущей задачи
    StatusError                                   // Упала, не перезапускается
)

type Session struct {
    ID                string              // "{project}/{session}" уникальный ключ
    ProjectName       string
    Config            SessionConfig
    Status            SessionStatus
    CurrentTask       string              // Текущая задача (из лога)
    Branch            string              // Текущая git-ветка (из лога)
    TasksDone         int                 // Завершённых задач в этом запуске
    StartedAt         time.Time
    LastActivity      time.Time
    RateLimitUntil    time.Time           // Когда снимется rate limit
    LogBuffer         []LogEntry          // Последние N строк (ring buffer)
    CLISessionID      string              // UUID сессии Claude CLI (для --resume)

    // Двунаправленный стриминг (секция 15)
    cmd               *exec.Cmd           // Текущий процесс
    cancel            context.CancelFunc
    stdinPipe         io.WriteCloser      // Запись в stdin Claude CLI
    inputCh           chan InputMessage    // Канал: UI → stdin
    mu                sync.Mutex

    // Permission handling (секция 16)
    pendingPermission *PermissionRequest  // Текущий запрос разрешения (или nil)
    permissionCh      chan PermResponse   // Канал: UI → permission handler
    runtimeRules      *RuntimeRuleSet    // "Allow for session" / "Allow similar"
}

type LogEntry struct {
    Time      time.Time
    Level     string    // "text" | "tool" | "error" | "result" | "system"
    Source    string    // "claude" | "manager"
    Message   string
    ToolName  string    // Для tool calls: Read, Edit, Bash, ...
    ToolInput string    // Сокращённый input (файл, команда)
}
```

### SQLite (история)

```sql
CREATE TABLE session_runs (
    id              INTEGER PRIMARY KEY,
    project         TEXT NOT NULL,
    session         TEXT NOT NULL,
    cli_session_id  TEXT,                -- UUID сессии Claude CLI (для --resume)
    model           TEXT,                -- "claude-sonnet-4-6"
    started_at      DATETIME NOT NULL,
    finished_at     DATETIME,
    status          TEXT NOT NULL,        -- "completed" | "error" | "stopped" | "rate_limited"
    tasks_done      INTEGER DEFAULT 0,
    exit_code       INTEGER,
    error_msg       TEXT,
    -- Метрики (из result event, см. секцию 18)
    total_cost_usd  REAL DEFAULT 0,
    input_tokens    INTEGER DEFAULT 0,
    output_tokens   INTEGER DEFAULT 0,
    cache_read_tokens    INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    num_turns       INTEGER DEFAULT 0,
    duration_ms     INTEGER DEFAULT 0
);

CREATE TABLE session_logs (
    id          INTEGER PRIMARY KEY,
    run_id      INTEGER REFERENCES session_runs(id),
    timestamp   DATETIME NOT NULL,
    level       TEXT NOT NULL,
    message     TEXT NOT NULL,
    tool_name   TEXT,
    tool_input  TEXT
);

CREATE INDEX idx_logs_run ON session_logs(run_id);
CREATE INDEX idx_runs_project ON session_runs(project, session);
```

---

## 6. Go Backend — ключевые компоненты

### 6.1 SessionManager

Центральный объект. Создаётся в `app.go`, привязан к Wails lifecycle.

```
SessionManager
├── sessions map[string]*Session     // "lumen/P1" → Session
├── config   *AppConfig
├── store    *Store
├── runtime  wails.Runtime           // Для EventsEmit
│
├── StartSession(projectName, sessionName)
├── StopSession(id, soft bool)       // soft=true → после текущей задачи
├── StopAll()
├── RestartSession(id)
├── ResumeSession(id)                // --resume с сохранённым session-id
├── GetAllSessions() []SessionState  // Для фронта
├── GetSessionLog(id, offset, limit) []LogEntry
├── GetHistory(project, limit) []SessionRun
│
├── SendMessage(id, message)         // Двунаправленный стриминг: отправить в stdin
├── RespondPermission(id, response)  // Ответить на permission request
├── GetPendingPermissions() []PermissionRequest
│
├── RunAnalysis(project, task) TaskPlan    // Pre-flight analysis
├── ApprovePlan(planID)
├── ExecutePlan(planID)
│
├── GetSessionMetrics(id) SessionMetrics   // Токены, стоимость
├── GetDailyCost(date) float64
├── GetProjectCost(project, days) float64
├── GetRateLimitStatus() RateLimitInfo
│
├── StartProject(projectName)        // Все сессии проекта
├── StopProject(projectName)
│
└── onSessionEvent(id, event)        // Внутренний callback → EventsEmit
```

### 6.2 Session (горутина)

Каждая сессия — бесконечный цикл в горутине. Использует двунаправленный стриминг (stdin/stdout) и обработку permission requests.

```
goroutine Session.Run(ctx):
    loop:
        if ctx.Done → return
        if softStop → return
        if maxTasks > 0 && tasksDone >= maxTasks → return
        if taskSource != "" && !hasTasks(taskSource) → return

        // Собрать аргументы CLI (см. секцию 14 — полный справочник флагов)
        args = buildCLIArgs(config)
        // Результат: claude -p
        //   --input-format stream-json
        //   --output-format stream-json
        //   --include-partial-messages
        //   --replay-user-messages
        //   --session-id <uuid>
        //   --model <model>
        //   --permission-mode <mode>
        //   [--fallback-model <model>]
        //   [--effort <level>]
        //   [--max-budget-usd <amount>]    (если > 0)
        //   [--worktree <name>]            (если use_worktree)
        //   [--allowed-tools <tools>]
        //   [--disallowed-tools <tools>]
        //   [--append-system-prompt <text>]
        //   [--add-dir <dirs>]

        cmd = exec.Command("claude", args...)
        cmd.Dir = projectPath

        stdoutPipe = cmd.StdoutPipe()
        stdinPipe  = cmd.StdinPipe()

        cmd.Start()
        status = Working
        emit("session:status", id, Working)

        // Отправить начальный промпт в stdin (stream-json)
        stdinPipe.Write(json({"type": "user_message", "message": prompt}))

        // Горутина: слушать inputCh и писать в stdin
        go inputWriter(stdinPipe, inputCh)

        // Читаем stdout построчно
        scanner = bufio.NewScanner(stdoutPipe)
        for scanner.Scan():
            line = scanner.Text()
            event = parseStreamJSON(line)

            switch event.Type:
            case "text", "tool_use", "result":
                entry = toLogEntry(event)
                appendToLogBuffer(entry)
                emit("session:log", id, entry)

            case "permission_request":
                // ⚠️ КРИТИЧНО: нельзя игнорировать!
                // Claude CLI ждёт ответа, сессия заблокирована
                handlePermissionRequest(event)
                // → проверяет auto-approve правила
                // → если нет правила → отправляет в UI → ждёт ответ
                // → отправляет decision в stdin

            case "rate_limit":
                detectRateLimit(event)

        exitCode = cmd.Wait()

        if rateLimited && exitCode != 0:
            status = RateLimited
            emit("session:status", id, RateLimited)
            sleep(rateLimitPause)
        else if exitCode != 0:
            status = Retrying
            sleep(retryDelay)
        else:
            tasksDone++
            emit("session:task_done", id, tasksDone)
            store.SaveRun(...)

        if !autoRestart:
            return

// Вспомогательная горутина: пересылает сообщения из inputCh в stdin
goroutine inputWriter(stdinPipe, inputCh):
    for msg := range inputCh:
        stdinPipe.Write(json(msg) + "\n")
```

### 6.3 Parser (stream-json)

Claude CLI с `--output-format stream-json` отдаёт по строке на событие:

```json
{"type": "assistant", "message": {"content": [{"type": "text", "text": "..."}, {"type": "tool_use", "name": "Read", "input": {"file_path": "..."}}]}}
{"type": "result", "result": "..."}
```

Парсер извлекает:
- **text** блоки → LogEntry{Level: "text", Message: текст}
- **tool_use** блоки → LogEntry{Level: "tool", ToolName: "Read", ToolInput: "/path/to/file"}
- **result** → LogEntry{Level: "result", Message: итог}
- Не-JSON строки → проверка на rate limit, прочее → LogEntry{Level: "system"}

Форматирование tool_use (сокращения для UI):
- `Bash` → показать первые 120 символов команды
- `Read` → показать путь файла
- `Edit` → показать путь файла
- `Write` → показать путь файла
- `Grep` → показать паттерн
- `Agent` → показать description

### 6.4 Rate limit detection

Проверка по нескольким сигналам (в порядке надёжности):
1. JSON: `{"type": "rate_limit_event", ...}` — точный сигнал
2. Текст в stdout/stderr: `"hit your limit"`, `"rate limit"` — только при `exitCode != 0`
3. Регулярка для извлечения времени сброса: `resets?\s+(\d{1,2}:\d{2}(?:am|pm)?)`

При rate limit:
- Статус → `RateLimited`
- UI показывает таймер обратного отсчёта
- Пауза `rate_limit_pause` секунд (из конфига, дефолт 300)
- После паузы → retry текущую задачу

---

## 7. Frontend — экраны и компоненты

### 7.1 Layout

```
┌─────────────────────────────────────────────────────────────────┐
│  Claude Session Manager                    [Settings] [─][□][✕] │
├─────────────┬───────────────────────────────────────────────────┤
│             │                                                   │
│  Sidebar    │  Main Panel                                       │
│  (250px)    │  (flex)                                           │
│             │                                                   │
├─────────────┴───────────────────────────────────────────────────┤
│  Status Bar                                                     │
└─────────────────────────────────────────────────────────────────┘
```

### 7.2 Sidebar

Дерево проектов с сессиями. Каждая сессия — кликабельная строка с индикатором статуса.

```
▼ lumen-browser            [▶ Start All] [⏹ Stop All]
    ● P1                   Working (14m)
    ● P2                   Working (8m)
    ◌ P3                   Idle
    ◌ P4                   Idle

▼ my-api                   [▶ Start All] [⏹ Stop All]
    ◌ backend              Idle
    ◌ frontend             Idle

[+ Add Project]
```

Индикаторы:
- `●` зелёный = Working
- `●` оранжевый = WaitingPermission (мигает)
- `●` жёлтый = RateLimited / Retrying
- `●` красный = Error
- `◌` серый = Idle / Stopped
- `●` синий = Starting / Analyzing

Клик по сессии → открывает её в Main Panel.

### 7.3 Main Panel — Session View

При клике на сессию:

**Верхняя полоса (Session Header):**
```
P1 — lumen-browser                          ● Working    14m 23s
Branch: p1-transform-fix    Task: BUG-021    Tasks done: 2
```

**Лог (основная область, 80% высоты):**
Скроллируемый лог с автоскроллом вниз. Цветовая подсветка:
- Серый — текст Claude (его рассуждения)
- Синий — tool calls (Read, Grep, Glob)
- Зелёный — Bash команды
- Оранжевый — Edit/Write (изменения файлов)
- Красный — ошибки
- Фиолетовый — Agent (субагенты)

Каждая строка: `[HH:MM:SS] <иконка типа> <сообщение>`

**Панель управления (под логом):**
```
[⏸ Pause] [⏹ Stop] [⏹ Stop after task] [🔄 Restart] [📋 Copy log] [🗑 Clear log]
```

- **Pause** — не убивает процесс; после завершения текущей задачи не запускает следующую
- **Stop** — немедленный kill процесса (SIGTERM → таймаут → SIGKILL)
- **Stop after task** — мягкая остановка: текущая задача доработает
- **Restart** — Stop + Start
- **Copy log** — весь лог в буфер обмена
- **Clear log** — очистить визуальный буфер (в SQLite остаётся)

### 7.4 Main Panel — History View

Табличный вид завершённых задач по проекту:

```
Session  │ Started          │ Duration │ Tasks │ Status
─────────┼──────────────────┼──────────┼───────┼──────────
P1       │ 22.05 14:32      │ 1h 12m   │ 4     │ Completed
P2       │ 22.05 14:32      │ 0h 45m   │ 2     │ Rate limited
P1       │ 22.05 12:00      │ 2h 03m   │ 7     │ Completed
```

Клик по строке → раскрывает лог этого запуска.

### 7.5 Settings (модальное окно)

**Global:**
- Путь к `claude` CLI
- Тема (dark/light)
- Retry delay, rate limit pause
- Log retention

**Per-project:**
- Name, Path (с кнопкой выбора папки)
- Список сессий (добавить/удалить/редактировать)

**Per-session:**
- Name, Prompt (multiline textarea)
- auto_restart, max_tasks, dangerously_skip_permissions
- task_source (опционально)
- Пре/пост хуки (команды shell)

### 7.6 Status Bar

```
Active: 3/6 │ ⚠️ Waiting: 1 │ Rate limited: 0 │ Errors: 0 │ Tasks today: 12 │ Uptime: 2h 34m
```

`⚠️ Waiting: N` — кликабельно, открывает PermissionQueue. Видно только когда N > 0.

---

## 8. Wails Bindings (Go ↔ Frontend)

### Go → Frontend (события)

```go
runtime.EventsEmit(ctx, "session:status", SessionStatusEvent{
    ID:     "lumen/P1",
    Status: "working",
})

runtime.EventsEmit(ctx, "session:log", SessionLogEvent{
    ID:    "lumen/P1",
    Entry: LogEntry{...},
})

runtime.EventsEmit(ctx, "session:task_done", SessionTaskEvent{
    ID:        "lumen/P1",
    TasksDone: 3,
})

runtime.EventsEmit(ctx, "session:rate_limit", RateLimitEvent{
    ID:    "lumen/P1",
    Until: time.Now().Add(5 * time.Minute),
})

// Permission request — сессия ждёт ответа пользователя
runtime.EventsEmit(ctx, "session:permission", PermissionRequestEvent{
    ID:          "lumen/P1",
    Request:     PermissionRequest{
        Tool:    "Bash",
        Command: "npm install --save-dev @types/node",
        RiskLevel: "medium",
    },
})

// Pre-flight analysis завершён — план готов
runtime.EventsEmit(ctx, "analysis:plan_ready", PlanReadyEvent{
    SessionID: "lumen/P1",
    Plan:      TaskPlan{...},
})
```

### Frontend → Go (вызовы)

```typescript
// Эти функции автогенерируются Wails из экспортированных методов Go
import { StartSession, StopSession, GetAllSessions, ... } from '../wailsjs/go/main/App';

await StartSession("lumen-browser", "P1");
await StopSession("lumen/P1", true);  // soft stop
const sessions = await GetAllSessions();
const logs = await GetSessionLog("lumen/P1", 0, 500);
const history = await GetHistory("lumen-browser", 50);

// Отправить сообщение в работающую сессию (двунаправленный стриминг)
await SendMessage("lumen/P1", "Используй Google OAuth2 вместо GitHub");

// Permission responses
await RespondPermission("lumen/P1", {requestId: "req-123", decision: "allow"});
await RespondPermission("lumen/P1", {requestId: "req-123", decision: "allow_always"});

// Pre-flight analysis
const plan = await RunAnalysis("lumen-browser", "Переписать модуль авторизации");
await ApprovePlan(plan.id);
await ExecutePlan(plan.id);

// Конфиг
await AddProject({name: "new-project", path: "D:\\..."});
await UpdateSession("lumen/P1", {prompt: "new prompt..."});
await RemoveProject("old-project");
```

---

## 9. Хуки (pre/post)

Опциональные shell-команды, выполняемые до/после каждой задачи сессии.

```toml
[[project.session]]
name = "P1"
pre_task_hook = "git fetch origin"
post_task_hook = "cargo clippy -p lumen-layout -- -D warnings"
```

- `pre_task_hook` — перед запуском `claude -p`. Если exit code != 0 → не запускать, retry после паузы.
- `post_task_hook` — после успешного завершения задачи (exit code 0). Информационный — результат записывается в лог, но не влияет на цикл.

Хуки запускаются в `cwd = project.path`.

---

## 10. Этапы реализации

### Этап 1: Каркас (1 день)

1. `wails init -n claude-manager -t svelte-ts`
2. Структура папок: `internal/config`, `internal/session`, `internal/store`
3. `config.go` — загрузка TOML, дефолты
4. `config.example.toml` — рабочий пример
5. Минимальный `app.go` с Wails lifecycle
6. Frontend: пустой layout (sidebar + main panel)
7. Сборка и запуск — пустое окно с заголовком

### Этап 2: Управление процессами (1-2 дня)

1. `session.go` — запуск `claude` с двунаправленным стримингом (stdin/stdout)
2. `input.go` — запись в stdin: user messages, permission responses
3. `parser.go` — парсинг stream-json, извлечение text/tool_use/result/permission_request
4. `manager.go` — SessionManager: Start/Stop/GetAll/SendMessage
5. Сборка CLI-аргументов из SessionConfig (все флаги из секции 14)
6. Rate limit detection
7. Retry-логика (пауза при ошибке, пауза при rate limit)
8. Auto-restart цикл (задача завершилась → проверить условия → следующая)
9. Биндинги Wails: экспортировать методы SessionManager

### Этап 2.5: Permission handling (1 день)

1. `permission/handler.go` — перехват permission_request из stream-json
2. `permission/rules.go` — матчинг правил из TOML и runtime
3. `permission/queue.go` — очередь ожидающих разрешений
4. Отправка permission response в stdin
5. StatusWaitingPermission — новый статус сессии
6. Timeout и эскалация (уведомления)
7. Биндинги Wails: RespondPermission, GetPendingPermissions

### Этап 3: Реалтайм UI (1-2 дня)

1. `Sidebar.svelte` — дерево проектов/сессий из GetAllSessions()
2. `SessionCard.svelte` — заголовок с метриками
3. `LogStream.svelte` — подписка на `session:log` события, рендер с подсветкой
4. `SessionInput.svelte` — поле ввода для отправки сообщений в сессию
5. `PermissionBanner.svelte` — баннер запроса разрешения поверх лога
6. `PermissionQueue.svelte` — глобальная очередь разрешений
7. Кнопки Start/Stop/Pause/Restart → вызов Go-функций
8. Индикаторы статуса (цветные точки, включая WaitingPermission)
9. Автоскролл лога с возможностью заморозить (scroll up = freeze)
10. Status Bar внизу (с индикатором Waiting)

### Этап 4: Persistence (0.5 дня)

1. `store.go` — SQLite: инициализация, миграции
2. Запись каждого завершённого run в `session_runs`
3. Запись логов в `session_logs` (batch insert, не каждую строку)
4. `History.svelte` — таблица завершённых запусков

### Этап 5: Настройки через UI (1 день)

1. `ProjectSettings.svelte` — добавить/редактировать проект
2. `SessionSettings.svelte` — добавить/редактировать сессию
3. Глобальные настройки
4. Сохранение в TOML → перезагрузка конфига
5. Выбор директории проекта (Wails dialog API)

### Этап 6: Pre-flight Analysis (1-2 дня)

1. `analysis/preflight.go` — запуск analyst-сессии (Haiku, --permission-mode plan)
2. `analysis/schema.go` — JSON Schema для структурированного ответа
3. `analysis/plan.go` — TaskPlan: парсинг, execution order, зависимости
4. `PlanReview.svelte` — UI для просмотра/редактирования плана
5. Execution engine: запуск подзадач по плану с передачей контекста
6. Хранение планов в SQLite (task_plans, plan_subtasks)
7. Биндинги Wails: RunAnalysis, ApprovePlan, ExecutePlan

### Этап 7: Оптимизация токенов (1-2 дня)

1. `optimization/context.go` — мониторинг контекста per-turn, авто-рестарт по порогу
2. `optimization/cache.go` — tracking cache efficiency, warming delay при старте сессий
3. `optimization/loop.go` — детекция зацикливания (повторяющиеся tool calls)
4. `optimization/routing.go` — auto model routing по результатам pre-flight analysis
5. `--exclude-dynamic-system-prompt-sections` при >1 сессии
6. `--max-turns` из конфига
7. Per-turn cost строки в логе (опционально)
8. Optimization dashboard в UI
9. Алерты: низкая cache efficiency, зацикливание, контекст > порога

### Этап 8: Полировка (1 день)

1. Тёмная/светлая тема
2. Горячие клавиши (Ctrl+1..4 — переключение сессий)
3. Трей-иконка (minimize to tray)
4. Уведомления (task done, error, rate limit, permission) — нативные Windows toast
5. Поиск по логам (Ctrl+F)
6. Экспорт лога в файл
7. Авто-очистка старых логов в SQLite (по `log_retention_days`)

---

## 11. Сборка и дистрибуция

```bash
# Dev-режим (hot reload фронта)
wails dev

# Продакшен-сборка
wails build

# Результат: build/bin/claude-manager.exe (один файл, ~15-20 MB)
```

Один `.exe`, не требует установки. WebView2 встроен в Windows 10/11.
Для Windows 7/8 — Wails может встроить WebView2 bootstrapper.

---

## 12. Что НЕ входит в v1

- Интеграция с Linear/GitHub Issues/Jira (источники задач)
- Веб-интерфейс (remote access)
- Multi-machine (управление Claude на другой машине)
- Автоматический git merge / PR creation
- Расписание (cron-like запуск)
- MCP серверы per-session (`--mcp-config`)
- Custom agents (`--agent`, `--agents`)
- Плагины per-session (`--plugin-dir`)
- Встроенный diff viewer для изменённых файлов

Эти фичи — кандидаты для v2, если v1 окажется полезным.

**Перенесено в v1 (по сравнению с первоначальным планом):**
- ~~Метрики токенов~~ → секция 18 (Claude CLI отдаёт `total_cost_usd` и usage в stream-json)
- Permission handling → секция 16
- Pre-flight analysis → секция 17
- Двунаправленный стриминг → секция 15
- Resume/continue → секция 19.1
- Отслеживание файлов → секция 19.2

---

## 13. Открытые вопросы

1. **Нужен ли multi-tab для логов?** Показывать несколько сессий одновременно (split view) или одну за раз достаточно?
2. **Нужно ли редактирование промптов с подсветкой синтаксиса?** Или textarea достаточно?
3. **Notifications:** Windows toast или in-app достаточно?
4. **Нужна ли группировка задач?** Например, "запустить P1+P2 вместе, P3+P4 вместе" одной кнопкой.
5. **Авторизация:** если несколько пользователей на одной машине — нужны ли профили?

---

## 14. CLI-флаги Claude Code — полный справочник

Менеджер должен поддерживать все релевантные флаги Claude CLI. Ниже — полный список с указанием, какие используются.

### 14.1 Режимы запуска и ввода/вывода

| Флаг | Поддержка | Описание |
|---|---|---|
| `-p, --print` | ✅ Базовый | Non-interactive режим |
| `--output-format stream-json` | ✅ Базовый | Потоковый JSON-вывод |
| `--input-format stream-json` | ✅ v1 | Двунаправленный стриминг — отправка сообщений в stdin |
| `--include-partial-messages` | ✅ v1 | Чанки по мере генерации (для UI стриминга текста) |
| `--include-hook-events` | ⬚ v2 | Lifecycle-события хуков в потоке |
| `--replay-user-messages` | ✅ v1 | Эхо отправленных сообщений для подтверждения доставки |
| `--verbose` | ✅ Базовый | Подробный вывод |
| `--json-schema <schema>` | ✅ v1 | Структурированный вывод (для pre-flight analysis) |

### 14.2 Управление сессиями

| Флаг | Поддержка | Описание |
|---|---|---|
| `--session-id <uuid>` | ✅ v1 | Менеджер сам генерирует UUID для каждой сессии |
| `-r, --resume <session-id>` | ✅ v1 | Возобновить конкретную сессию с полным контекстом |
| `-c, --continue` | ✅ v1 | Продолжить последний разговор в директории |
| `--fork-session` | ⬚ v2 | Ответвление от сессии при resume |
| `-n, --name <name>` | ✅ v1 | Имя сессии (отображается в CLI) |
| `--from-pr <url>` | ⬚ v2 | Возобновить сессию привязанную к PR |
| `--no-session-persistence` | ⬚ v2 | Не сохранять сессию на диск |

### 14.3 Модель и производительность

| Флаг | Поддержка | Описание |
|---|---|---|
| `--model <model>` | ✅ v1 | Модель: `opus`, `sonnet`, `haiku` или полное имя |
| `--fallback-model <model>` | ✅ v1 | Автопереключение при перегрузке основной модели |
| `--effort <level>` | ✅ v1 | `low`, `medium`, `high`, `xhigh`, `max` |
| `--max-budget-usd <amount>` | ✅ v1 | Лимит расходов на сессию (опционально) |

### 14.4 Разрешения

| Флаг | Поддержка | Описание |
|---|---|---|
| `--permission-mode <mode>` | ✅ v1 | `default`, `acceptEdits`, `auto`, `plan`, `dontAsk`, `bypassPermissions` |
| `--dangerously-skip-permissions` | ✅ Базовый | Полный bypass (legacy, заменяется `--permission-mode`) |
| `--allowedTools <tools>` | ✅ v1 | Белый список: `"Bash(npm test) Edit Read"` |
| `--disallowedTools <tools>` | ✅ v1 | Чёрный список: `"Bash(rm *) Bash(git push *)"` |
| `--tools <tools>` | ⬚ v2 | Ограничить набор доступных инструментов |

### 14.5 Контекст и промпты

| Флаг | Поддержка | Описание |
|---|---|---|
| `--system-prompt <prompt>` | ⬚ v2 | Полностью заменить системный промпт |
| `--append-system-prompt <prompt>` | ✅ v1 | Добавить инструкции к системному промпту |
| `--add-dir <dirs>` | ✅ v1 | Дополнительные директории для доступа |
| `--agent <name>` | ⬚ v2 | Использовать предопределённого агента |
| `--agents <json>` | ⬚ v2 | Определить кастомных агентов inline |
| `--bare` | ⬚ v2 | Минимальный режим без автоматизации |

### 14.6 Оптимизация токенов

| Флаг | Поддержка | Описание |
|---|---|---|
| `--max-budget-usd <amount>` | ✅ v1 | Лимит расходов на сессию в USD |
| `--exclude-dynamic-system-prompt-sections` | ✅ v1 | Общий кэш system prompt между сессиями |
| `--max-turns` | ✅ v1 | Лимит turns (защита от зацикливания) |
| `--effort <level>` | ✅ v1 | Контроль объёма thinking-токенов |
| `--fallback-model <model>` | ✅ v1 | Переключение при перегрузке (избежание retry) |

### 14.7 Worktree и окружение

| Флаг | Поддержка | Описание |
|---|---|---|
| `-w, --worktree [name]` | ✅ v1 | Claude сам создаёт git worktree — нативная изоляция |
| `--mcp-config <path>` | ⬚ v2 | Подключить MCP-серверы к сессии |
| `--strict-mcp-config` | ⬚ v2 | Только указанные MCP, игнорить остальные |
| `--plugin-dir <path>` | ⬚ v2 | Подключить плагины из директории |
| `--settings <file>` | ⬚ v2 | Кастомный settings.json для сессии |

---

## 15. Двунаправленный стриминг

### 15.1 Проблема

В текущем плане сессия запускается через `claude -p "prompt"` — один промпт, один ответ. Менеджер **не может** взаимодействовать с работающей сессией: отправить follow-up, ответить на вопрос, перенаправить.

### 15.2 Решение: `--input-format stream-json`

Claude CLI поддерживает **двунаправленный канал**:

```
Manager → stdin (stream-json)  → Claude CLI
Manager ← stdout (stream-json) ← Claude CLI
```

Запуск:
```bash
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --include-partial-messages \
  --replay-user-messages \
  --session-id <uuid> \
  --model <model> \
  --permission-mode <mode>
```

Manager пишет в stdin:
```json
{"type": "user_message", "message": "Начни с задачи BUG-021 из STATUS-P1.md"}
```

Claude отвечает через stdout:
```json
{"type": "assistant", "message": {"content": [...]}}
{"type": "tool_use", "name": "Read", "input": {...}}
{"type": "result", "result": "..."}
```

### 15.3 Что это даёт менеджеру

| Возможность | Как |
|---|---|
| Отправить follow-up | Написать в stdin новый `user_message` |
| Ответить на вопрос Claude | Claude спросил уточнение → UI показывает → пользователь вводит → stdin |
| Перенаправить Claude | "Стоп, сделай иначе через X" |
| Интерактивный чат в GUI | Поле ввода под логом сессии |
| Permission responses | Ответы на запросы разрешений через stdin |

### 15.4 Архитектура потоков (обновлённая)

```
┌─────────────────────────────────────────────────────────────┐
│  Session goroutine                                          │
│                                                             │
│  ┌──────────┐     ┌───────────┐     ┌─────────────────┐    │
│  │ stdin    │◄────│ inputCh   │◄────│ UI / Auto-rules │    │
│  │ (writer) │     │ (chan)     │     │                 │    │
│  └──────────┘     └───────────┘     └─────────────────┘    │
│                                                             │
│  ┌──────────┐     ┌───────────┐     ┌─────────────────┐    │
│  │ stdout   │────▶│ parser    │────▶│ EventsEmit      │    │
│  │ (reader) │     │           │     │ → Frontend       │    │
│  └──────────┘     │ detect:   │     └─────────────────┘    │
│                   │ • text     │                             │
│                   │ • tool_use │                             │
│                   │ • result   │                             │
│                   │ • perm_req │──▶ Permission handler       │
│                   │ • question │──▶ Question handler         │
│                   │ • rate_lim │──▶ Rate limit handler       │
│                   └───────────┘                             │
└─────────────────────────────────────────────────────────────┘
```

### 15.5 Go-реализация

```go
type Session struct {
    // ... существующие поля ...

    stdinPipe     io.WriteCloser      // Для записи в stdin
    inputCh       chan InputMessage    // Канал входящих сообщений
    permissionCh  chan PermResponse    // Канал ответов на permission
}

// InputMessage — сообщение от UI в сессию
type InputMessage struct {
    Type    string `json:"type"`    // "user_message"
    Message string `json:"message"`
}

// Отправка сообщения в работающую сессию
func (s *Session) SendMessage(msg string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.Status != StatusWorking && s.Status != StatusWaitingPermission {
        return fmt.Errorf("session %s is not active", s.ID)
    }

    input := InputMessage{Type: "user_message", Message: msg}
    data, _ := json.Marshal(input)
    data = append(data, '\n')

    _, err := s.stdinPipe.Write(data)
    return err
}
```

### 15.6 UI: поле ввода в сессии

Под логом сессии появляется поле ввода (disabled когда сессия не активна):

```
┌─────────────────────────────────────────────────────────────┐
│  [Log stream area...]                                       │
│  ...                                                        │
│  [14:32:15] Claude: Какой OAuth2 провайдер использовать?    │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│  > Используй Google OAuth2 с PKCE flow            [Send ▶] │
└─────────────────────────────────────────────────────────────┘
```

---

## 16. Система управления разрешениями

### 16.1 Проблема

Без `--dangerously-skip-permissions` Claude Code **останавливается и ждёт ответа** при каждом потенциально опасном действии. Если менеджер не обрабатывает эти запросы — сессия зависает навсегда.

Это **не опциональная фича**. Любая сессия без `bypassPermissions` будет генерировать permission requests, и менеджер **обязан** их обработать.

### 16.2 Permission Flow

```
Claude CLI (stdout stream-json)
    │
    ├── обычные события (text, tool_use, result)  →  лог
    └── permission_request                         →  ПЕРЕХВАТ
         │
         ▼
    Manager (Go backend)
         │
         ├── Проверить auto-approve правила (TOML + runtime)
         │     ├── Совпало с "allow"  → автоматический ответ в stdin
         │     ├── Совпало с "deny"   → автоматический отказ в stdin
         │     └── Совпало с "ask" или нет правила ↓
         │
         ├── Установить статус: StatusWaitingPermission
         ├── EventsEmit("session:permission", PermissionRequest{...})
         │
         ▼
    Frontend (UI)
         │
         ├── Звуковой сигнал + нотификация
         ├── Баннер поверх лога с деталями запроса
         ├── Глобальная очередь (если несколько сессий ждут)
         ├── Кнопки: [Allow] [Deny] [Allow similar] [Allow for session] [Always allow]
         │
         ▼
    User нажимает кнопку
         │
         ▼
    Manager отправляет ответ в stdin (stream-json)
         │
         ▼
    Claude CLI продолжает работу
```

### 16.3 Модели данных

```go
// PermissionRequest — запрос разрешения от Claude CLI
type PermissionRequest struct {
    ID            string    `json:"id"`
    SessionID     string    `json:"session_id"`
    Timestamp     time.Time `json:"timestamp"`
    Tool          string    `json:"tool"`          // "Bash", "Edit", "Write", "McpTool"
    Description   string    `json:"description"`   // Что Claude хочет сделать
    Command       string    `json:"command"`       // Для Bash: полная команда
    FilePath      string    `json:"file_path"`     // Для Edit/Write: путь к файлу
    RiskLevel     string    `json:"risk_level"`    // "low", "medium", "high"
    WaitingSince  time.Time `json:"waiting_since"`
}

// PermissionResponse — ответ пользователя
type PermissionResponse struct {
    RequestID  string `json:"request_id"`
    Decision   string `json:"decision"`   // "allow", "deny", "allow_session", "allow_always"
}

// PermissionRule — правило авто-одобрения (из конфига или runtime)
type PermissionRule struct {
    Tool      string `toml:"tool"`       // "Bash", "Edit", "*"
    Pattern   string `toml:"pattern"`    // glob/regex для команды/файла
    Decision  string `toml:"decision"`   // "allow", "deny", "ask"
}
```

### 16.4 Новый статус сессии

```go
const (
    StatusIdle              SessionStatus = iota  // Не запущена
    StatusStarting                                // Запускается
    StatusWorking                                 // Claude работает
    StatusWaitingPermission                       // Ждёт ответа на permission
    StatusRateLimited                             // Пауза по rate limit
    StatusRetrying                                // Пауза перед retry
    StatusStopping                                // Ждёт завершения задачи
    StatusError                                   // Упала
)
```

### 16.5 Конфигурация правил автоматического одобрения

```toml
[[project.session]]
name = "P1"
permission_mode = "default"    # Спрашивать разрешения

  # Правила авто-одобрения (проверяются в порядке объявления)
  # tool: "Bash", "Edit", "Write", "McpTool", "*" (все)
  # pattern: glob для команды (Bash) или пути (Edit/Write)
  # decision: "allow" (авто-да), "deny" (авто-нет), "ask" (всегда спросить)

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "npm test*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "npm run build*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "cargo build*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "cargo test*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "cargo clippy*"
  decision = "allow"

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "rm *"
  decision = "deny"           # Всегда запрещать удаление

  [[project.session.permission_rule]]
  tool = "Bash"
  pattern = "git push*"
  decision = "ask"            # Всегда спрашивать (даже если есть "allow all")

  [[project.session.permission_rule]]
  tool = "Edit"
  pattern = "*"
  decision = "allow"          # Все правки файлов — OK

  [[project.session.permission_rule]]
  tool = "Write"
  pattern = "*.go"
  decision = "allow"          # Новые .go файлы — OK

  [[project.session.permission_rule]]
  tool = "Write"
  pattern = "*.sh"
  decision = "ask"            # Скрипты — спросить
```

### 16.6 Типы ответов в UI

| Кнопка | Действие | Что запоминается |
|---|---|---|
| **Allow** | Разрешить этот конкретный запрос | Ничего |
| **Deny** | Запретить этот конкретный запрос | Ничего |
| **Allow similar** | Разрешить + создать runtime-правило по паттерну | В памяти до рестарта менеджера |
| **Allow for session** | Разрешить все такие до конца этой сессии | В памяти до конца сессии |
| **Always allow** | Разрешить + сохранить правило в TOML | Постоянно |
| **Always deny** | Запретить + сохранить правило в TOML | Постоянно |

### 16.7 Go-реализация обработки разрешений

```go
func (s *Session) handlePermissionRequest(req PermissionRequest) {
    // 1. Проверить правила из конфига (TOML)
    for _, rule := range s.Config.PermissionRules {
        if rule.Matches(req) {
            switch rule.Decision {
            case "allow":
                s.respondPermission(req.ID, "allow")
                s.appendLog(LogEntry{Level: "system",
                    Message: fmt.Sprintf("Auto-approved: %s %s", req.Tool, req.Command)})
                return
            case "deny":
                s.respondPermission(req.ID, "deny")
                s.appendLog(LogEntry{Level: "system",
                    Message: fmt.Sprintf("Auto-denied: %s %s", req.Tool, req.Command)})
                return
            case "ask":
                break // Перейти к UI
            }
        }
    }

    // 2. Проверить runtime-правила ("Allow for session", "Allow similar")
    if s.runtimeRules.Allows(req) {
        s.respondPermission(req.ID, "allow")
        return
    }

    // 3. Нет подходящего правила → отправить в UI и ждать
    s.mu.Lock()
    s.Status = StatusWaitingPermission
    s.pendingPermission = &req
    s.mu.Unlock()

    runtime.EventsEmit(s.ctx, "session:permission", req)
    runtime.EventsEmit(s.ctx, "session:status", SessionStatusEvent{
        ID: s.ID, Status: "waiting_permission",
    })

    // 4. Блокирующее ожидание ответа от UI
    response := <-s.permissionCh

    // 5. Отправить ответ Claude CLI через stdin
    s.respondPermission(req.ID, response.Decision)

    s.mu.Lock()
    s.Status = StatusWorking
    s.pendingPermission = nil
    s.mu.Unlock()

    // 6. Запомнить правило если нужно
    switch response.Decision {
    case "allow_session":
        s.runtimeRules.Add(req.Tool, req.Pattern(), "allow")
    case "allow_always":
        s.runtimeRules.Add(req.Tool, req.Pattern(), "allow")
        s.manager.SavePermissionRule(s.Config, req.Tool, req.Pattern(), "allow")
    case "deny_always":
        s.manager.SavePermissionRule(s.Config, req.Tool, req.Pattern(), "deny")
    }
}

// respondPermission отправляет ответ в stdin Claude CLI
func (s *Session) respondPermission(reqID, decision string) {
    resp := map[string]string{
        "type":       "permission_response",
        "request_id": reqID,
        "decision":   decision,
    }
    data, _ := json.Marshal(resp)
    data = append(data, '\n')
    s.stdinPipe.Write(data)
}
```

### 16.8 UI компоненты

**PermissionBanner.svelte** — баннер поверх лога сессии:
```
┌──────────────────────────────────────────────────────────────────┐
│ ⚠️  P1 ждёт разрешения (32 сек)                                 │
│                                                                  │
│  🔧 Bash: npm install --save-dev @types/node                     │
│  Риск: 🟡 средний                                                │
│                                                                  │
│  [✓ Allow]  [✗ Deny]  [✓ Allow similar]  [✓ Always allow]        │
└──────────────────────────────────────────────────────────────────┘
```

**PermissionQueue.svelte** — глобальная очередь (когда несколько сессий ждут):
```
┌──────────────────────────────────────────────────────────────┐
│  Permission Queue (3 pending)                     [Settings] │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  🔴 P1 — Bash: git push origin main          (2m 14s)       │
│     [Allow] [Deny]                                           │
│                                                              │
│  🟡 P2 — Edit: src/auth/provider.go           (45s)         │
│     [Allow] [Deny] [Allow all Edit]                          │
│                                                              │
│  🟢 P3 — Bash: cargo test                     (12s)         │
│     [Allow] [Deny] [Allow similar]                           │
│                                                              │
│  ─────────────────────────────────────────────────            │
│  Quick actions:  [Allow all safe ✓]  [Deny all ✗]            │
└──────────────────────────────────────────────────────────────┘
```

### 16.9 Timeout и эскалация

```toml
[settings]
permission_notify_after = 30       # Секунд до усиленного уведомления
permission_timeout = 0             # 0 = ждать бесконечно (default)
permission_timeout_action = "deny" # deny | pause_session
permission_native_notification = true  # Windows toast когда приложение свёрнуто
permission_sound = true            # Звуковой сигнал
```

Эскалация по времени:
- **0s** — запрос появился в UI, звук
- **30s** — баннер мигает жёлтым
- **1m** — Windows toast notification (если свёрнуто)
- **5m** — красный баннер "Session P1 blocked for 5 minutes!"
- **permission_timeout** — автоматическое действие (если настроено)

### 16.10 Sidebar интеграция

```
▼ lumen-browser
    ● P1                   ⚠️ Waiting permission (45s)
    ● P2                   Working (8m)
    ◌ P3                   Idle
```

### 16.11 StatusBar интеграция

```
Active: 3/6 │ ⚠️ Waiting: 2 │ Rate limited: 0 │ Tasks today: 12
                 ↑
         Кликабельно — открывает очередь разрешений
```

### 16.12 Таблица permission_mode

| Режим CLI | Поведение в менеджере |
|---|---|
| `bypassPermissions` | Нет запросов, всё разрешено. Менеджер ничего не делает |
| `acceptEdits` | Edit/Write → auto-allow. Bash → через UI-очередь + auto-approve правила |
| `auto` | Классификатор Claude решает. Опасные → через UI |
| `default` | Всё через UI (с учётом auto-approve правил из TOML) |
| `dontAsk` | Не спрашивает, но опасные действия блокирует |
| `plan` | Claude только планирует, ничего не выполняет |

---

## 17. Pre-flight Analysis — анализ задачи перед запуском

### 17.1 Проблема

Пользователь даёт задачу: *"Перепиши модуль авторизации с JWT на OAuth2"*. Claude начинает работать, тратит 150k токенов контекста, упирается в лимит, теряет контекст, делает половину работы криво. Потрачено $8 и 40 минут впустую.

### 17.2 Решение: двухфазный запуск

```
Фаза 1: Анализ (лёгкая сессия)     →  Фаза 2: Исполнение (рабочие сессии)

 "Можно за 1 сессию?"               Да  →  1 сессия, запуск
       │
       │ Нет
       ▼
 "Разбей на подзадачи"             →  N сессий с планом
       │
       ▼
 Показать план в UI                →  User approve/edit → Launch
```

### 17.3 Analyst Session

Менеджер запускает **лёгкую аналитическую сессию** перед основной работой:

```bash
claude -p \
  --model haiku \
  --effort medium \
  --permission-mode plan \
  --json-schema '<structured plan schema>' \
  --output-format json \
  "Analyze this task: <user_task>"
```

Ключевые флаги:
- `--permission-mode plan` — Claude **только анализирует**, ничего не выполняет
- `--model haiku` — дешёвая модель для анализа
- `--json-schema` — структурированный ответ, парсится менеджером
- `--max-budget-usd` — опциональный лимит расходов на анализ

### 17.4 Оценка стоимости анализа

| Проект | Tool calls | Haiku | Sonnet | Opus |
|---|---|---|---|---|
| **Маленький** (3-5 файлов) | 4-5 | ~$0.03 | ~$0.12 | ~$0.60 |
| **Средний** (10-15 файлов) | 8-12 | ~$0.08 | ~$0.31 | ~$1.56 |
| **Большой** (20-30 файлов) | 15-25 | ~$0.15 | ~$0.65 | ~$3.20 |

Рекомендация: **Haiku** для анализа. Хватает для оценки масштаба и декомпозиции, при стоимости $0.03-0.15.

### 17.5 JSON Schema для аналитика

```json
{
  "type": "object",
  "properties": {
    "feasibility": {
      "type": "object",
      "properties": {
        "single_session": { "type": "boolean" },
        "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
        "reasoning": { "type": "string" },
        "estimated_complexity": {
          "type": "string",
          "enum": ["trivial", "small", "medium", "large", "epic"]
        },
        "estimated_files_affected": { "type": "integer" },
        "estimated_tokens": { "type": "integer" },
        "risks": {
          "type": "array",
          "items": { "type": "string" }
        }
      }
    },
    "recommended_approach": {
      "type": "string",
      "enum": ["single_session", "sequential_sessions", "parallel_sessions", "mixed"]
    },
    "recommended_model": { "type": "string" },
    "recommended_effort": { "type": "string" },
    "subtasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "name": { "type": "string" },
          "prompt": { "type": "string" },
          "depends_on": { "type": "array", "items": { "type": "string" } },
          "model": { "type": "string" },
          "effort": { "type": "string" },
          "use_worktree": { "type": "boolean" },
          "estimated_tokens": { "type": "integer" },
          "files_to_touch": { "type": "array", "items": { "type": "string" } }
        }
      }
    },
    "execution_order": {
      "type": "array",
      "description": "Groups of task IDs. Within a group — parallel. Groups run sequentially.",
      "items": { "type": "array", "items": { "type": "string" } }
    },
    "shared_context": { "type": "string" }
  }
}
```

### 17.6 Конфигурация

```toml
[settings]
# Pre-flight analysis
preflight_analysis = true               # Включить анализ перед запуском
preflight_model = "haiku"               # Модель для анализа (дешёвая)
preflight_max_budget = 0.20             # Опционально: лимит расходов. 0 = без лимита
preflight_auto_approve_single = true    # Если single_session=true → запускать сразу
preflight_complexity_threshold = "medium"  # Анализировать только задачи >= порога
```

Per-session override:
```toml
[[project.session]]
name = "P1"
preflight = "auto"    # "always" | "auto" | "never"
                      # always — всегда анализ → план → approve
                      # auto — анализ → если simple, запуск сразу; если нет → план
                      # never — как сейчас, без анализа
```

Бюджет опционален. Если `preflight_max_budget = 0` или поле не указано — `--max-budget-usd` не передаётся, Claude работает без ограничений.

### 17.7 UI: Plan Review Screen

```
┌─────────────────────────────────────────────────────────────────────┐
│  Task Analysis                                        [Re-analyze] │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ⚠ Задача НЕ помещается в одну сессию                              │
│                                                                     │
│  Complexity: ████████░░ Large    Files: ~18    Tokens: ~180k        │
│  Confidence: 85%                                                    │
│                                                                     │
│  Risks:                                                             │
│  • Параллельные изменения auth и handlers могут конфликтовать       │
│  • Тесты зависят от нового auth — нужна последовательность          │
│                                                                     │
├─────────────────────────────────────────────────────────────────────┤
│  Proposed Plan (3 sessions, sequential)                             │
│                                                                     │
│  ┌─ Step 1 ─────────────────────────────────────────────────────┐   │
│  │  auth-core │ Opus │ High │ ~60k tokens │ 4 files             │   │
│  │  OAuth2 core module                              [Edit] [✕]  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│       │                                                             │
│       ▼                                                             │
│  ┌─ Step 2 ─────────────────────────────────────────────────────┐   │
│  │  handlers │ Sonnet │ Medium │ ~50k tokens │ 3 files          │   │
│  │  Update HTTP handlers                            [Edit] [✕]  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│       │                                                             │
│       ▼                                                             │
│  ┌─ Step 3 ─────────────────────────────────────────────────────┐   │
│  │  tests │ Sonnet │ Medium │ ~45k tokens │ 2 files             │   │
│  │  Tests for OAuth2                                [Edit] [✕]  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  Est. cost: $4.20    Est. time: ~25 min                             │
│                                                                     │
│  [+ Add Step]  [Reorder]                                            │
│                                                                     │
│        [ Cancel ]    [ Edit Plan ]    [ Execute Plan ]              │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 17.8 Передача контекста между подзадачами

Когда задача разбита на последовательные подзадачи, каждая следующая сессия получает результат предыдущей:

```
Session 1 ("auth-core") завершилась
    │
    ├── Менеджер собирает: какие файлы изменены, summary результата
    │
    ▼
Session 2 ("handlers-update") получает в --append-system-prompt:
    "Предыдущая сессия 'auth-core' завершила:
     - Создан internal/auth/provider.go (OAuth2 Google/GitHub)
     - Обновлён internal/auth/middleware.go
     - Интерфейс AuthService сохранён
     Продолжай с этого состояния."
```

### 17.9 Модели данных для плана

```go
type TaskPlan struct {
    ID              string           `json:"id"`
    OriginalTask    string           `json:"original_task"`
    Analysis        AnalysisResult   `json:"analysis"`
    Subtasks        []PlannedSubtask `json:"subtasks"`
    ExecutionOrder  [][]string       `json:"execution_order"`
    Status          PlanStatus       `json:"status"`       // draft | approved | executing | completed
    SharedContext   string           `json:"shared_context"`
    CreatedAt       time.Time        `json:"created_at"`
    TotalCostUSD    float64          `json:"total_cost_usd"`
}

type PlannedSubtask struct {
    ID              string        `json:"id"`
    Name            string        `json:"name"`
    Prompt          string        `json:"prompt"`
    DependsOn       []string      `json:"depends_on"`
    Model           string        `json:"model"`
    Effort          string        `json:"effort"`
    UseWorktree     bool          `json:"use_worktree"`
    SessionID       string        `json:"session_id"`       // UUID когда создана сессия
    Status          SubtaskStatus `json:"status"`            // pending | running | completed | failed
    ResultSummary   string        `json:"result_summary"`
    FilesChanged    []string      `json:"files_changed"`
}
```

### 17.10 SQLite — хранение планов

```sql
CREATE TABLE task_plans (
    id              INTEGER PRIMARY KEY,
    project         TEXT NOT NULL,
    original_task   TEXT NOT NULL,
    analysis_json   TEXT NOT NULL,
    status          TEXT NOT NULL,       -- "draft" | "approved" | "executing" | "completed"
    created_at      DATETIME NOT NULL,
    completed_at    DATETIME,
    total_cost_usd  REAL,
    total_tokens    INTEGER
);

CREATE TABLE plan_subtasks (
    id              INTEGER PRIMARY KEY,
    plan_id         INTEGER REFERENCES task_plans(id),
    subtask_id      TEXT NOT NULL,
    name            TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    depends_on      TEXT,               -- JSON array of subtask IDs
    model           TEXT,
    session_run_id  INTEGER REFERENCES session_runs(id),
    status          TEXT NOT NULL,
    result_summary  TEXT,
    files_changed   TEXT                -- JSON array
);
```

### 17.11 Системный промпт для аналитика

```
You are a task analyst for Claude Code sessions. Your job is to evaluate
whether a programming task can be completed in a single Claude Code session
(~200k context window) or needs to be decomposed.

Consider:
1. Number of files that need reading + modification
2. Complexity of reasoning required
3. Whether subtasks have dependencies or can run in parallel
4. Risk of context overflow degrading quality
5. Cost optimization (use cheaper models for simpler subtasks)

Rules:
- Tasks touching <=5 files and one module → usually single_session
- Tasks touching >10 files across multiple modules → usually needs splitting
- Refactors with mechanical changes → parallel sessions with worktrees
- Tasks with testing phase → sequential (tests depend on implementation)
- Always prefer fewer, larger sessions over many tiny ones
- Each subtask prompt must be self-contained and actionable
```

---

## 18. Метрики токенов и стоимости

### 18.1 Источник данных

Claude CLI в `--output-format stream-json` отдаёт полную информацию о токенах и стоимости. Менеджеру не нужно считать самому — достаточно парсить и агрегировать.

**В каждом `assistant` message — per-turn usage:**
```json
{
  "type": "assistant",
  "message": {
    "model": "claude-sonnet-4-6",
    "usage": {
      "input_tokens": 15230,
      "cache_creation_input_tokens": 16634,
      "cache_read_input_tokens": 8200,
      "output_tokens": 342,
      "service_tier": "standard"
    }
  }
}
```

**В финальном `result` — итоги сессии:**
```json
{
  "type": "result",
  "total_cost_usd": 0.0624,
  "duration_ms": 7416,
  "duration_api_ms": 6947,
  "num_turns": 1,
  "usage": {
    "input_tokens": 15230,
    "cache_creation_input_tokens": 16634,
    "cache_read_input_tokens": 8200,
    "output_tokens": 342,
    "server_tool_use": {
      "web_search_requests": 0,
      "web_fetch_requests": 0
    },
    "cache_creation": {
      "ephemeral_1h_input_tokens": 16634,
      "ephemeral_5m_input_tokens": 0
    }
  },
  "modelUsage": {
    "claude-sonnet-4-6": {
      "inputTokens": 15230,
      "outputTokens": 342,
      "cacheReadInputTokens": 8200,
      "cacheCreationInputTokens": 16634,
      "costUSD": 0.0624,
      "contextWindow": 200000,
      "maxOutputTokens": 32000
    }
  }
}
```

**В `rate_limit_event` — утилизация лимита:**
```json
{
  "type": "rate_limit_event",
  "rate_limit_info": {
    "status": "allowed_warning",
    "resetsAt": 1779584400,
    "rateLimitType": "seven_day",
    "utilization": 0.88,
    "isUsingOverage": false,
    "surpassedThreshold": 0.75
  }
}
```

### 18.2 Что парсить и хранить

```go
// TokenUsage — per-turn usage (из каждого assistant message)
type TokenUsage struct {
    InputTokens              int     `json:"input_tokens"`
    CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
    CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
    OutputTokens             int     `json:"output_tokens"`
    ServiceTier              string  `json:"service_tier"`
}

// SessionResult — итоги из result event
type SessionResult struct {
    TotalCostUSD   float64                `json:"total_cost_usd"`
    DurationMs     int64                  `json:"duration_ms"`
    DurationApiMs  int64                  `json:"duration_api_ms"`
    NumTurns       int                    `json:"num_turns"`
    Usage          TokenUsage             `json:"usage"`
    ModelUsage     map[string]ModelUsage  `json:"modelUsage"`
    StopReason     string                 `json:"stop_reason"`
}

// ModelUsage — разбивка по модели
type ModelUsage struct {
    InputTokens              int     `json:"inputTokens"`
    OutputTokens             int     `json:"outputTokens"`
    CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
    CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
    CostUSD                  float64 `json:"costUSD"`
    ContextWindow            int     `json:"contextWindow"`
    MaxOutputTokens          int     `json:"maxOutputTokens"`
}

// RateLimitInfo — утилизация лимита
type RateLimitInfo struct {
    Status             string  `json:"status"`
    ResetsAt           int64   `json:"resetsAt"`        // Unix timestamp
    RateLimitType      string  `json:"rateLimitType"`   // "seven_day", "daily"
    Utilization        float64 `json:"utilization"`     // 0.0 - 1.0
    IsUsingOverage     bool    `json:"isUsingOverage"`
    SurpassedThreshold float64 `json:"surpassedThreshold"`
}
```

### 18.3 Агрегация метрик

Менеджер агрегирует данные на нескольких уровнях:

| Уровень | Что показывать |
|---|---|
| **Per turn** | Токены in/out, cache hit rate, задержка API |
| **Per session run** | total_cost_usd, общие токены, длительность, количество turns |
| **Per session** | Сумма за все runs, средняя стоимость задачи |
| **Per project** | Сумма всех сессий, расходы за день/неделю/месяц |
| **Global** | Все проекты, общий бюджет, rate limit utilization |

### 18.4 SQLite — расширение таблиц

```sql
-- Добавить в session_runs
ALTER TABLE session_runs ADD COLUMN total_cost_usd REAL DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN input_tokens INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN output_tokens INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN cache_read_tokens INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN cache_creation_tokens INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN num_turns INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN duration_ms INTEGER DEFAULT 0;
ALTER TABLE session_runs ADD COLUMN model TEXT;

-- Агрегированные метрики за день (для дашборда)
CREATE TABLE daily_metrics (
    date        TEXT NOT NULL,          -- "2026-05-22"
    project     TEXT NOT NULL,
    total_cost  REAL DEFAULT 0,
    total_input_tokens  INTEGER DEFAULT 0,
    total_output_tokens INTEGER DEFAULT 0,
    total_runs  INTEGER DEFAULT 0,
    total_tasks INTEGER DEFAULT 0,
    PRIMARY KEY (date, project)
);
```

### 18.5 UI: метрики в реалтайме

**Session Header (расширенный):**
```
P1 — lumen-browser                          ● Working    14m 23s
Branch: p1-fix    Task: BUG-021    Turns: 8    Tokens: 45.2k in / 3.1k out
Cost: $0.42    Cache hit: 73%    Context: ████████░░ 78%
```

**Context usage bar** — критически важно:
- Показывает процент заполнения контекстного окна (200k)
- Зелёный до 60%, жёлтый 60-80%, красный >80%
- Парсится из `contextWindow` (200000) и текущих `input_tokens`

**StatusBar (расширенный):**
```
Active: 3/6 │ ⚠️ Waiting: 1 │ Cost today: $12.40 │ Rate limit: 88% │ Tasks: 12
```

**Cost Dashboard (новый экран, доступен из Settings или отдельной вкладкой):**
```
┌─────────────────────────────────────────────────────────────────┐
│  Cost Dashboard                              Period: [This week]│
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Total: $47.20                                                  │
│  ┌──────────────────────────────────────────────────────┐       │
│  │ ████████████████████                    $32.10       │ Opus  │
│  │ ████████████                            $12.30       │ Sonnet│
│  │ ███                                     $2.80        │ Haiku │
│  └──────────────────────────────────────────────────────┘       │
│                                                                 │
│  By project:                                                    │
│  lumen-browser    $34.50  (73%)   ███████████████░░░░░          │
│  my-api           $12.70  (27%)   █████░░░░░░░░░░░░░░          │
│                                                                 │
│  Cache efficiency: 68% reads / 32% creation                     │
│  Avg cost per task: $1.85                                       │
│  Rate limit utilization: 88% (7-day)                            │
│                                                                 │
│  Daily breakdown:                                               │
│  Mon $8.20 │ Tue $12.10 │ Wed $6.30 │ Thu $9.40 │ Fri $11.20  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 18.6 Алерты по бюджету

```toml
[settings]
daily_budget_alert = 20.0          # Уведомление при превышении $20/день. 0 = выкл
weekly_budget_alert = 100.0        # Уведомление при превышении $100/неделю. 0 = выкл
rate_limit_alert_threshold = 0.80  # Уведомление при утилизации > 80%
```

```go
// Проверка после каждого result event
func (m *SessionManager) checkBudgetAlerts(sessionID string, result SessionResult) {
    dailyCost := m.store.GetDailyCost(time.Now())

    if m.config.Settings.DailyBudgetAlert > 0 && dailyCost > m.config.Settings.DailyBudgetAlert {
        runtime.EventsEmit(m.ctx, "alert:budget", BudgetAlert{
            Type:    "daily",
            Current: dailyCost,
            Limit:   m.config.Settings.DailyBudgetAlert,
            Message: fmt.Sprintf("Daily spending $%.2f exceeds limit $%.2f",
                dailyCost, m.config.Settings.DailyBudgetAlert),
        })
    }
}
```

### 18.7 Wails bindings для метрик

```typescript
// Frontend → Go
const metrics = await GetSessionMetrics("lumen/P1");           // Метрики текущей сессии
const dailyCost = await GetDailyCost("2026-05-22");            // Расходы за день
const projectCost = await GetProjectCost("lumen-browser", 7);  // За последние 7 дней
const rateLimit = await GetRateLimitStatus();                   // Утилизация лимита
```

---

## 19. Дополнительные возможности

### 19.1 Resume / Continue сессии

Каждая сессия получает `--session-id <uuid>`, который менеджер генерирует и сохраняет. При рестарте менеджер может **возобновить** сессию вместо создания новой — сохраняя весь контекст.

**Конфигурация:**
```toml
[[project.session]]
name = "P1"
resume_on_restart = true    # При авто-рестарте использовать --resume вместо нового запуска
```

**Логика:**
```
Сессия завершилась (exit code 0, задача выполнена)
    │
    ├── resume_on_restart = false → новый запуск с новым session-id
    └── resume_on_restart = true  → claude -p --resume <old-session-id> --input-format stream-json ...
                                    Отправить новый промпт через stdin
```

**Когда resume нельзя:**
- Сессия завершилась с ошибкой (crash) — контекст может быть повреждён
- Сессия была kill -9 — состояние неопределённо
- Явная команда пользователя "New session"

**UI:** Кнопка в панели управления:
```
[⏸ Pause] [⏹ Stop] [🔄 Restart] [🔄 Resume] [📋 Copy log]
```
`Resume` — продолжить с контекстом. `Restart` — начать заново.

### 19.2 Отслеживание изменённых файлов

Менеджер парсит tool_use события из stream-json и собирает список изменённых файлов:

```go
// FileChange — одно изменение файла
type FileChange struct {
    FilePath  string    `json:"file_path"`
    Action    string    `json:"action"`    // "edit", "create", "delete"
    Tool      string    `json:"tool"`      // "Edit", "Write", "Bash"
    Timestamp time.Time `json:"timestamp"`
}

// Парсинг из stream-json
func (p *Parser) detectFileChange(event StreamEvent) *FileChange {
    switch event.ToolName {
    case "Edit":
        return &FileChange{FilePath: event.Input.FilePath, Action: "edit", Tool: "Edit"}
    case "Write":
        return &FileChange{FilePath: event.Input.FilePath, Action: "create", Tool: "Write"}
    case "Bash":
        // Парсинг команд: rm, mv, cp, touch, mkdir
        if strings.HasPrefix(event.Input.Command, "rm ") {
            return &FileChange{FilePath: extractPath(event.Input.Command), Action: "delete", Tool: "Bash"}
        }
    }
    return nil
}
```

**UI — панель изменённых файлов (в Session View):**
```
┌─ Changed Files (7) ─────────────────────────────────┐
│  ✏️  src/auth/provider.go          Edit    14:32:15  │
│  ✏️  src/auth/middleware.go        Edit    14:33:42  │
│  ✨  src/auth/oauth2.go            Create  14:34:10  │
│  ✏️  src/api/auth_handler.go       Edit    14:35:28  │
│  ✏️  src/api/user_handler.go       Edit    14:36:01  │
│  🗑️  src/auth/jwt.go              Delete  14:36:55  │
│  ✏️  go.mod                        Edit    14:37:12  │
│                                                      │
│  [View git diff] [Revert all] [Revert selected]     │
└──────────────────────────────────────────────────────┘
```

`[View git diff]` — открывает diff в системном приложении или встроенном просмотрщике.
`[Revert all]` — `git checkout` всех изменённых файлов (с подтверждением).

### 19.3 Зависимости между сессиями

Сессия может зависеть от другой — запускается автоматически после завершения.

```toml
[[project.session]]
name = "refactor"
prompt = "Refactor the auth module..."
permission_mode = "acceptEdits"

[[project.session]]
name = "tests"
prompt = "Write tests for the refactored auth module..."
depends_on = "refactor"         # Запуск после завершения "refactor"
inherit_context = true          # Получить summary того, что сделала "refactor"
```

**Логика:**
```go
func (m *SessionManager) onSessionCompleted(sessionID string) {
    // Найти сессии, зависящие от завершённой
    for _, s := range m.sessions {
        if s.Config.DependsOn == sessionID && s.Status == StatusIdle {
            if s.Config.InheritContext {
                // Передать summary предыдущей сессии
                summary := m.buildContextSummary(sessionID)
                s.Config.SystemPromptAppend += "\n\n" + summary
            }
            m.StartSession(s.ProjectName, s.Config.Name)
        }
    }
}
```

**UI в Sidebar:**
```
▼ lumen-browser
    ● refactor              Working (14m)
    ◌ tests                 Idle (waiting for: refactor)
      └─ depends on: refactor
```

### 19.4 Уведомления с контекстом

Не просто "P1 завершился", а информативные уведомления:

```go
type Notification struct {
    SessionID string
    Type      string    // "task_done", "error", "permission", "rate_limit", "budget"
    Title     string    // Краткий заголовок
    Body      string    // Детали
    Actions   []string  // Кнопки в notification
}

// Примеры:
// task_done:
//   Title: "P1 завершил задачу BUG-021"
//   Body:  "Изменено 3 файла, 8 turns, $0.42. Все тесты пройдены."
//
// error:
//   Title: "P2 упал: compilation error"
//   Body:  "error[E0308]: mismatched types in main.rs:42"
//
// permission:
//   Title: "P3 ждёт разрешения"
//   Body:  "Bash: cargo publish --dry-run"
//   Actions: ["Allow", "Deny", "Open"]
//
// budget:
//   Title: "Дневной бюджет превышен"
//   Body:  "Потрачено $21.40 из лимита $20.00"
```

Уведомления отправляются как:
- **In-app баннер** — всегда
- **Windows toast** — если `permission_native_notification = true` и приложение свёрнуто
- **Tray icon badge** — мигающая иконка в трее

### 19.5 Фильтрация и поиск логов

**Фильтры по типу:**
```
┌─ Log Filters ──────────────────────────────────────┐
│  [All] [Text] [Tool calls] [Bash] [Errors] [System]│
│                                                     │
│  Tool: [All ▼]   Risk: [All ▼]   Search: [______]  │
└─────────────────────────────────────────────────────┘
```

- Переключатели фильтрации в реальном времени (не перезагружая лог)
- Фильтр по инструменту: "покажи только Bash-вызовы"
- Фильтр по риску: "только опасные действия"
- Текстовый поиск (Ctrl+F) — по видимому логу + по SQLite-истории

**Поиск по истории (SQLite):**
```typescript
// Полнотекстовый поиск по всем логам проекта
const results = await SearchLogs("lumen-browser", "compilation error", {
    dateFrom: "2026-05-20",
    dateTo: "2026-05-22",
    sessions: ["P1", "P2"],
    levels: ["error"],
    limit: 50,
});
```

### 19.6 Экспорт и отчёты

**Экспорт лога сессии:**
- Markdown (`.md`) — форматированный лог с подсветкой
- Plain text (`.txt`) — сырой текст
- JSON (`.json`) — полный stream-json для анализа

**Дневной отчёт:**
```markdown
# Claude Manager — отчёт за 22.05.2026

## lumen-browser
- **P1**: 4 задачи, 2h 12m, $8.40
  - BUG-021: исправлен race condition в layout engine
  - BUG-022: исправлен краш при пустом DOM
  - FEAT-15: добавлена поддержка CSS grid
  - FEAT-16: оптимизация рендеринга (в процессе)
- **P2**: 2 задачи, 1h 05m, $4.20
  - TEST-08: тесты для layout engine
  - TEST-09: тесты для CSS grid

## Итого
- Задач: 6
- Время: 3h 17m
- Расход: $12.60
- Cache efficiency: 71%
```

**Генерация:** `await GenerateDailyReport("2026-05-22")` → файл или показ в UI.

### 19.7 Шаблоны сессий

Переиспользуемые конфигурации для типичных ролей:

```toml
# Шаблоны определяются глобально
[[template]]
name = "developer"
permission_mode = "acceptEdits"
model = "sonnet"
effort = "high"
auto_restart = true
use_worktree = true
preflight = "auto"

  [[template.permission_rule]]
  tool = "Edit"
  pattern = "*"
  decision = "allow"

  [[template.permission_rule]]
  tool = "Bash"
  pattern = "cargo *"
  decision = "allow"

[[template]]
name = "reviewer"
permission_mode = "plan"
model = "opus"
effort = "high"
auto_restart = false
preflight = "never"

[[template]]
name = "tester"
permission_mode = "acceptEdits"
model = "sonnet"
effort = "medium"
auto_restart = false

# Использование в сессии:
[[project.session]]
name = "P1"
template = "developer"              # Наследует все настройки шаблона
prompt = "Прочитай STATUS-P1.md..."
# Можно переопределить отдельные поля:
model = "opus"                      # Override шаблона
```

### 19.8 Init event — информация при запуске

В `init` событии Claude CLI отдаёт полную информацию об окружении:

```json
{
  "type": "system",
  "subtype": "init",
  "session_id": "16e0200b-...",
  "model": "claude-sonnet-4-6",
  "tools": ["Bash", "Edit", "Read", "Grep", "Glob", "Write", ...],
  "mcp_servers": [{"name": "github", "status": "connected"}],
  "permissionMode": "default",
  "claude_code_version": "2.1.118",
  "cwd": "D:\\RustProjects\\lumen-browser"
}
```

Менеджер парсит это и показывает в Session Header:
```
P1 — lumen-browser                   claude v2.1.118 │ sonnet-4-6 │ acceptEdits
```

---

## 20. Оптимизация расхода токенов

### 20.1 Как растут расходы — корень проблемы

Claude Code при каждом turn отправляет **весь контекст** (system prompt + вся история сообщений + все tool results). Контекст **накапливается** — каждый последующий turn дороже предыдущего.

Реальные данные из 3-turn сессии (Sonnet, задача "подсчитай строки файла"):

```
Turn 1: cache_read=10,804  cache_create=5,841   fresh=2    output=8
Turn 2: cache_read=16,645  cache_create=153     fresh=1    output=1
Turn 3: cache_read=16,798  cache_create=143     fresh=1    output=1
─────────────────────────────────────────────────────────────────
Total:  cache_read=44,247  cache_create=6,137   fresh=4    output=221
Cost: $0.04
```

Ключевое наблюдение: **3 turn'а, тривиальная задача, $0.04**. Потому что system prompt = 16k токенов, и он пересылается каждый turn.

**Прогноз расходов для длинных сессий (Sonnet):**

| Turns | Context (приблизит.) | С кэшированием | Без кэширования |
|---|---|---|---|
| 5 | ~25k | ~$0.10 | ~$0.30 |
| 15 | ~60k | ~$0.50 | ~$1.50 |
| 30 | ~120k | ~$1.50 | ~$5.00 |
| 50 | ~180k | ~$4.00 | ~$15.00 |
| 80+ | 200k (предел) | ~$8.00+ | ~$30.00+ |

**Вывод:** кэширование снижает стоимость в 3-5 раз, но расход всё равно растёт квадратично с числом turns. Менеджер ОБЯЗАН активно управлять этим.

### 20.2 Пять уровней кэша

Claude Code использует многоуровневое кэширование:

```
┌─────────────────────────────────────────────────────────────┐
│  Цены за 1M токенов (Sonnet)                                │
├─────────────────────────────────────────────────────────────┤
│  Cache read (5m ephemeral)      $0.30    (10x дешевле input)│
│  Cache read (1h ephemeral)      $0.30    (10x дешевле input)│
│  Fresh input                    $3.00    (базовая цена)     │
│  Cache write (5m ephemeral)     $3.75    (25% дороже input) │
│  Cache write (1h ephemeral)     $3.75    (25% дороже input) │
│  Output                        $15.00                       │
├─────────────────────────────────────────────────────────────┤
│  Приоритет: максимизировать cache reads, минимизировать     │
│  cache writes и fresh input                                  │
└─────────────────────────────────────────────────────────────┘
```

**Что кэшируется:**
- System prompt → `ephemeral_1h` (живёт 1 час)
- Ранние сообщения → `ephemeral_5m` (живёт 5 минут)

**Следствие для менеджера:**
- Если между turn'ами прошло >5 минут — кэш ранних сообщений протух, они будут re-created (дорого)
- System prompt кэш живёт 1 час — если все сессии используют одинаковый system prompt, они **делят один кэш**

### 20.3 Стратегии оптимизации — подробный анализ

---

#### Стратегия 1: Авто-рестарт по порогу контекста

**Суть:** менеджер следит за заполнением контекстного окна (200k токенов) и автоматически перезапускает сессию когда контекст становится слишком большим — потому что каждый новый turn пересылает ВЕСЬ контекст, и чем он больше, тем дороже каждый turn.

**Механика:**
```
Turn 1:  context ~20k  → cost ~$0.01/turn
Turn 15: context ~80k  → cost ~$0.03/turn
Turn 30: context ~150k → cost ~$0.05/turn   ← в 5 раз дороже, чем в начале
Turn 40: context ~190k → cost ~$0.06/turn   ← предел, скоро context overflow

При рестарте на turn 30:
Turn 31 (новая сессия): context ~20k → cost ~$0.01/turn  ← снова дёшево
```

**Режимы рестарта:**
- `resume` — `--resume <session-id>`. Claude CLI сам загрузит summary предыдущей сессии. Контекст сбрасывается, но Claude "помнит" что делал. Лучший вариант.
- `fresh` — полностью новая сессия. Потеря контекста, но гарантированно чистый старт. Подходит для auto_restart сессий с повторяющимися задачами.
- `ask` — показать пользователю выбор в UI.
- `off` — не рестартить (пользователь управляет сам).

**Экономия (Sonnet, 50-turn сессия):**
```
Без авто-рестарта:
  Turns 1-50, контекст растёт до ~180k
  Суммарный input: ~3M tokens
  Стоимость input: ~$3.50 (с кэшированием)

С авто-рестартом на 75% (turn ~30):
  Turns 1-30: ~1.5M tokens → $1.80
  Restart + Turns 31-50: ~0.6M tokens → $0.70
  Стоимость input: ~$2.50
  Экономия: ~$1.00 (29%)
```

**Плюсы:**
- Предотвращает квадратичный рост стоимости
- Предотвращает context overflow (Claude деградирует после ~80% контекста)
- С `resume` — минимальная потеря контекста
- Полностью автоматическая, не требует внимания пользователя

**Минусы:**
- `resume` добавляет ~5-10 секунд на перезапуск процесса
- При `fresh` — полная потеря контекста, Claude может повторить уже сделанное
- Рестарт в середине сложной задачи может сломать ход мысли
- Неточный расчёт: `usage` из stream-json показывает токены одного turn, а не суммарный размер контекста

**Когда использовать:** всегда (рекомендуемый default). Для длинных auto_restart сессий — обязательно.

**Когда НЕ использовать:** для коротких одноразовых задач (5-10 turns), где контекст не успевает вырасти.

**Конфигурация:**
```toml
[optimization]
context_restart_threshold = 0.75     # Рестарт при > 75%
context_warn_threshold = 0.60        # Предупреждение при > 60%
context_restart_mode = "resume"      # resume | fresh | ask | off
```

**Взаимодействие с другими стратегиями:**
- Дополняет Стратегию 5 (max-turns): авто-рестарт по контексту может сработать раньше, чем max-turns
- Конфликтует с длинными задачами: если задача требует >100 turns и весь контекст критичен, рестарт может помешать. В таком случае → `ask` или `off`

---

#### Стратегия 2: Общий кэш system prompt (`--exclude-dynamic-system-prompt-sections`)

**Суть:** Claude Code по умолчанию включает в system prompt информацию, специфичную для текущей машины/директории: cwd, переменные окружения, git status. Это делает system prompt **уникальным** для каждой сессии — каждая платит за cache creation. Флаг перемещает эти секции в первое user message, делая system prompt **общим** для всех сессий.

**Механика:**
```
БЕЗ флага (default):
  P1: system_prompt = [инструкции 10k] + [cwd=/project-A, env, git status 5k] = 15k (уникальный)
  P2: system_prompt = [инструкции 10k] + [cwd=/project-B, env, git status 5k] = 15k (уникальный)
  P3: system_prompt = [инструкции 10k] + [cwd=/project-A, env, git status 5k] = 15k (уникальный*)
  
  * Даже P1 и P3 в одном проекте могут иметь разный git status!
  Каждая сессия: cache WRITE 15k × $3.75/1M = $0.056

С флагом:
  P1: system_prompt = [инструкции 10k] (ОБЩИЙ)  |  user_msg += [cwd, env, git 5k]
  P2: system_prompt = [инструкции 10k] (ОБЩИЙ)  |  user_msg += [cwd, env, git 5k]
  P3: system_prompt = [инструкции 10k] (ОБЩИЙ)  |  user_msg += [cwd, env, git 5k]
  
  Первая сессия: cache WRITE 10k × $3.75/1M = $0.037
  Остальные:     cache READ  10k × $0.30/1M = $0.003 каждая
```

**Экономия (Sonnet, за один цикл запуска):**

| Кол-во сессий | Без флага | С флагом | Экономия | % |
|---|---|---|---|---|
| 2 | $0.112 | $0.040 | $0.072 | 64% |
| 4 | $0.224 | $0.046 | $0.178 | 79% |
| 8 | $0.448 | $0.058 | $0.390 | 87% |

Это экономия **только на system prompt при запуске**. При авто-рестартах каждый рестарт экономит столько же.

**Для типичного рабочего дня (4 сессии, 10 рестартов каждая):**
- Без флага: 40 × $0.056 = $2.24
- С флагом: 4 × $0.037 + 36 × $0.003 = $0.256
- **Экономия: $1.98/день только на system prompt**

**Плюсы:**
- Значительная экономия при параллельных сессиях
- Нулевая стоимость включения — один флаг CLI
- Эффект усиливается с количеством сессий и рестартов
- Полностью прозрачно для Claude — никакого влияния на качество работы

**Минусы:**
- Dynamic секции перемещаются в user message — это увеличивает размер первого сообщения на ~5k токенов
- Не работает с `--system-prompt` (полная замена системного промпта)
- Эффект есть только при >1 сессии. Для одиночной сессии — бесполезно
- Если проекты на разных машинах с разными Claude Code версиями — base system prompt может отличаться

**Когда использовать:** всегда при запуске 2+ сессий параллельно.

**Когда НЕ использовать:** при одной сессии (нет с кем делить кэш).

**Конфигурация:**
```toml
[optimization]
exclude_dynamic_system_prompt = true  # --exclude-dynamic-system-prompt-sections
```

---

#### Стратегия 3: Cache warming — порядок запуска сессий

**Суть:** кэш создаётся при первом обращении и живёт определённое время (system prompt — 1 час, ранние сообщения — 5 минут). Если запустить N сессий одновременно, каждая может не успеть увидеть кэш другой и все N заплатят за cache creation. Последовательный запуск с задержкой 2-5 секунд позволяет первой сессии "прогреть" кэш для остальных.

**Механика:**
```
Одновременный запуск (все 4 в t=0):
  t=0s: P1 → API call → cache MISS → cache WRITE 16k ($0.060)
  t=0s: P2 → API call → cache MISS → cache WRITE 16k ($0.060)  ← не видит кэш P1!
  t=0s: P3 → API call → cache MISS → cache WRITE 16k ($0.060)
  t=0s: P4 → API call → cache MISS → cache WRITE 16k ($0.060)
  Итого: 4 × $0.060 = $0.240

Последовательный запуск (с задержкой 3 сек):
  t=0s: P1 → cache WRITE 16k ($0.060)
  t=3s: P2 → cache READ  16k ($0.005)  ← кэш P1 уже существует!
  t=6s: P3 → cache READ  16k ($0.005)
  t=9s: P4 → cache READ  16k ($0.005)
  Итого: $0.060 + 3 × $0.005 = $0.075
```

**Экономия:** $0.165 на запуске 4 сессий. При 10 рестартах в день: **~$1.65/день**.

**Плюсы:**
- Простая реализация (одна строка `time.Sleep`)
- Гарантированная экономия при >1 сессии
- Комбинируется со Стратегией 2 для максимального эффекта

**Минусы:**
- Добавляет задержку: N сессий × delay секунд. Для 8 сессий с 3-сек задержкой = 21 секунда до запуска последней
- Не гарантирует cache hit — зависит от инфраструктуры Anthropic (кэш может быть per-region)
- Бесполезно если сессии на разных моделях (Haiku и Sonnet имеют разные system prompts)
- Бесполезно без Стратегии 2: если system prompt уникальный для каждой сессии, cache warming не поможет

**Когда использовать:** вместе со Стратегией 2, при запуске 3+ сессий на одной модели.

**Когда НЕ использовать:** при одной сессии; при разных моделях; когда критична скорость запуска.

**Конфигурация:**
```toml
[optimization]
session_start_delay = 3     # Секунд между запуском. 0 = одновременно
```

**Оптимальная задержка:** 2-5 секунд. Меньше — кэш может не успеть создаться. Больше — лишнее ожидание.

---

#### Стратегия 4: Маршрутизация по модели (auto-routing)

**Суть:** разные задачи требуют разного уровня интеллекта. Использование Opus ($15/$75 за 1M tokens) для линтинга — расточительство. Haiku ($0.80/$4) справится не хуже и будет в **19x дешевле** по input и **19x** по output.

**Разница в стоимости:**

| Модель | Input / 1M | Output / 1M | 20-turn сессия | Разница vs Sonnet |
|---|---|---|---|---|
| **Haiku 3.5** | $0.80 | $4.00 | ~$0.15 | **20x дешевле** |
| **Sonnet 4** | $3.00 | $15.00 | ~$1.50 | baseline |
| **Opus 4** | $15.00 | $75.00 | ~$8.00 | **5x дороже** |

**Пример за рабочий день (10 задач):**

| Подход | Задачи | Стоимость |
|---|---|---|
| Всё на Opus | 10 × $8.00 | **$80.00** |
| Всё на Sonnet | 10 × $1.50 | **$15.00** |
| Smart routing | 3 Haiku + 5 Sonnet + 2 Opus | **$8.95** |

**Маршрутизация:**

| Тип задачи | Модель | Effort | Примеры |
|---|---|---|---|
| Тривиальные | Haiku + low | low | Форматирование, переименование, обновление версий, lint fix |
| Стандартные | Sonnet + medium | medium | Баг-фиксы, новые endpoints, тесты, рефакторинг модуля |
| Сложные | Sonnet + high | high | Многофайловые изменения, оптимизация, интеграции |
| Архитектурные | Opus + high | high | Новые подсистемы, миграции БД, сложные алгоритмы |

**Два режима маршрутизации:**

1. **Ручной** (в конфиге сессии): пользователь сам выбирает модель для каждой сессии
```toml
[[project.session]]
name = "linter"
model = "haiku"
effort = "low"
```

2. **Автоматический** (через pre-flight analysis): analyst-сессия оценивает сложность задачи и рекомендует модель. Менеджер может принять рекомендацию автоматически или показать пользователю.

**Плюсы:**
- Колоссальная экономия (до 20x разницы между Haiku и Opus)
- Haiku быстрее Opus — задачи выполняются быстрее
- Легко настроить per-session в конфиге

**Минусы:**
- Неверный выбор модели → плохой результат. Haiku на архитектурной задаче может зациклиться или сделать некачественно
- Auto-routing требует pre-flight analysis (дополнительная стоимость $0.03-0.15)
- Pre-flight analysis сам может ошибиться в оценке сложности
- Не все задачи легко классифицировать — "простой баг-фикс" может оказаться архитектурной проблемой

**Риски:**
- Haiku на сложной задаче: сессия потратит 30 turns безрезультатно → потеря $0.45 + времени
- Opus на простой задаче: переплата $6-7, но результат будет хороший (не критичный риск)

**Рекомендация:** начинать с ручного выбора. Auto-routing включать после накопления истории, когда менеджер "видел" типичные задачи.

**Конфигурация:**
```toml
[optimization]
auto_model_routing = false    # true = выбор модели по сложности (через pre-flight)
```

---

#### Стратегия 5: Ограничение turns (`--max-turns`)

**Суть:** жёсткий лимит на количество turns в одной сессии. Защита от зацикливания и неконтролируемого расхода. Claude CLI остановится после N turns, даже если задача не завершена.

**Почему это важно:**

```
Нормальная сессия:    15 turns → задача выполнена → $1.50
Зацикленная сессия:   80 turns → контекст overflow → задача НЕ выполнена → $8.00+

max_turns = 50 ограничивает:
Зацикленная сессия:   50 turns → лимит достигнут → $4.00 (экономия $4.00+)
```

**Адаптивный лимит:**

Менеджер анализирует историю завершённых задач и корректирует лимит:

```
История сессии P1:
  Run 1: 12 turns, completed
  Run 2: 18 turns, completed
  Run 3: 14 turns, completed
  Run 4: 11 turns, completed
  Среднее: 14 turns, max: 18

Адаптивный лимит: max(18) × 1.6 = 29 turns (вместо дефолтных 50)
```

**Что происходит при достижении лимита:**
1. Claude CLI завершается с exit code (не ошибка, а лимит)
2. Менеджер проверяет `auto_restart`:
   - `true` → начать новую сессию с тем же промптом (или `--resume`)
   - `false` → остановить, показать пользователю

**Плюсы:**
- Страховка от катастрофических расходов
- Простой, надёжный механизм (встроен в CLI)
- Адаптивный лимит подстраивается под реальные паттерны
- Не влияет на нормальные сессии (лимит выше обычного использования)

**Минусы:**
- Слишком низкий лимит → сессия прерывается посередине задачи
- При `auto_restart` + `resume` → может возникнуть бесконечный цикл рестартов если задача действительно требует >N turns
- Адаптивный лимит может "заблокироваться" если первые несколько задач были аномально короткими/длинными

**Рекомендация:** `max_turns_default = 50`, `max_turns_auto_adjust = true`. Для known-short задач (линтинг) — `max_turns = 15`.

**Конфигурация:**
```toml
[optimization]
max_turns_default = 50           # Дефолтный лимит
max_turns_auto_adjust = true     # Автокоррекция на основе истории

# Per-session override:
[[project.session]]
name = "linter"
max_turns = 15
```

---

#### Стратегия 6: Мониторинг cache efficiency

**Суть:** менеджер отслеживает соотношение cache reads / total input для каждого turn. Низкая cache efficiency означает, что деньги тратятся неоптимально — контекст не кэшируется и каждый turn стоит в 10x дороже, чем мог бы.

**Как считается:**
```
Cache efficiency = cache_read_input_tokens / (input_tokens + cache_read + cache_creation)

Хорошо:   efficiency > 70%  (большая часть контекста из кэша)
Нормально: efficiency 40-70% (смешанная ситуация)
Плохо:    efficiency < 40%  (мало кэш-хитов, дорого)
```

**Причины низкой efficiency:**

| Причина | Почему | Решение |
|---|---|---|
| Первый turn сессии | Кэш ещё не создан | Нормально, исправится само |
| Пауза >5 минут между turns | Кэш ранних сообщений протух | Увеличить темп работы или рестартить |
| Пауза >1 час | System prompt кэш протух | Рестартить |
| Разные модели | Каждая модель — свой кэш | Не смешивать модели в одной сессии |
| Уникальный system prompt | Нет с кем делить | Включить Стратегию 2 |
| Очень большие tool results | Новый контент не в кэше | Ожидаемо, не проблема |

**Экономия от высокой efficiency:**
```
Sonnet, turn с 80k контекстом:
  100% cache reads: 80k × $0.30/1M = $0.024
  0% cache reads:   80k × $3.00/1M = $0.240
  Разница: 10x
```

**Плюсы:**
- Делает стоимость прозрачной и понятной
- Позволяет выявлять проблемы (долгие паузы, кэш-промахи)
- Данные для оптимизации других стратегий

**Минусы:**
- Сам мониторинг не экономит — только показывает метрики
- Может генерировать лишние алерты (нормальная низкая efficiency на первых turns)
- Пользователь может не знать, что делать с метриками

**Рекомендация:** включить всегда (`show_cache_efficiency = true`), алерты при <40% после 5-го turn.

---

#### Стратегия 7: Effort level — контроль thinking-токенов

**Суть:** `--effort` управляет объёмом "размышлений" (thinking tokens) Claude. Thinking tokens — это output tokens, которые в **5x дороже** input (Sonnet: $15 vs $3). Снижение effort на простых задачах напрямую снижает количество дорогих output tokens.

**Влияние на стоимость (Sonnet, 20-turn сессия):**

| Effort | Thinking tokens/turn | Output cost/turn | Сессия (20 turns) | % от high |
|---|---|---|---|---|
| `low` | ~50 | $0.0008 | ~$0.60 | 40% |
| `medium` | ~200 | $0.003 | ~$0.90 | 60% |
| `high` | ~500 | $0.0075 | ~$1.50 | 100% |
| `max` | ~2000 | $0.03 | ~$3.50 | 233% |

**Когда какой effort:**

| Effort | Когда | Примеры | Риск при неверном выборе |
|---|---|---|---|
| `low` | Задача механическая, не требует рассуждений | Переименование, форматирование, обновление import'ов | Claude может пропустить edge case |
| `medium` | Стандартная задача, понятная логика | Типичный баг-фикс, новый endpoint по шаблону | Может не заметить архитектурную проблему |
| `high` | Задача требует анализа и планирования | Рефакторинг модуля, сложный баг, интеграция | Переплата на простых задачах |
| `max` | Критически сложная задача | Алгоритмы, миграция архитектуры, security fix | Значительная переплата |

**Плюсы:**
- Прямое снижение самой дорогой компоненты (output tokens)
- Простая настройка (один параметр)
- Не влияет на доступные инструменты или контекст — только на глубину рассуждений
- Быстрее: меньше thinking = быстрее ответ

**Минусы:**
- Слишком низкий effort → Claude делает ошибки, "не думая"
- Нет способа предсказать оптимальный effort заранее (кроме pre-flight analysis)
- Effort влияет на ВСЕ turns сессии — нельзя "думать больше" на сложном шаге и "меньше" на простом

**Рекомендация:** default = `medium`. Для known-simple сессий (линтинг, форматирование) = `low`. Для архитектурных = `high`. `max` — только по явному запросу.

**Комбинация с моделью:**
```toml
# Максимальная экономия для тривиальных задач:
model = "haiku"
effort = "low"
# → ~$0.05 за 20-turn сессию (vs $1.50 при Sonnet+high = 30x разница!)

# Максимальное качество:
model = "opus"
effort = "max"
# → ~$20.00 за 20-turn сессию
```

---

#### Стратегия 8: Детекция зацикливания

**Суть:** Claude иногда зацикливается — читает одни и те же файлы, выполняет одни и те же команды, не продвигаясь к решению. Каждый бесполезный turn стоит денег. Менеджер отслеживает паттерны tool calls и обнаруживает повторения.

**Типичные паттерны зацикливания:**

| Паттерн | Пример | Стоимость потери |
|---|---|---|
| Повторное чтение | `Read("src/auth.go")` 5 раз подряд | ~$0.15-0.25 за бесполезные turns |
| Redo edit | Edit файла → Read → Edit того же → Read → ... | ~$0.30-0.50 |
| Bash retry | Одна и та же команда, один и тот же exit code | ~$0.10-0.20 |
| Grep loop | Поиск одного и того же паттерна в разных вариациях | ~$0.20-0.40 |
| Agent spawn | Запуск Agent → тот же промпт → тот же результат | ~$1.00-3.00 (agent = много токенов) |

**Алгоритм детекции:**
```
Ring buffer: последние 20 tool calls → {tool:"Read", input:"src/auth.go"}
Для каждого нового tool call:
  Создать key = tool + ":" + normalized_input
  Подсчитать повторения key в буфере
  Если повторений >= threshold → ЗАЦИКЛИВАНИЕ
```

**Нормализация input:** для Bash → первые 80 символов команды (игнорировать аргументы); для Read → полный путь; для Edit → путь файла (без содержимого); для Grep → паттерн.

**Действия при обнаружении:**

| Действие | Как работает | Плюсы | Минусы |
|---|---|---|---|
| `warn` | Показать алерт в UI | Не прерывает работу | Пользователь может не заметить |
| `send_hint` | Отправить сообщение в stdin | Может "разблокировать" Claude | Claude может проигнорировать |
| `restart` | Перезапустить с модифицированным промптом | Гарантированный выход из цикла | Потеря прогресса |

**Текст hint'а (настраиваемый):**
```
"You seem to be repeating the same actions. The file src/auth.go has been read 4 times.
Try a different approach: either modify the file, look at a different file, or
summarize what you've learned and explain what's blocking you."
```

**Ложные срабатывания:**
- Claude легитимно перечитывает файл после Edit (проверяет результат) — это НЕ зацикливание
- Grep с разными паттернами по одному файлу — нормальное исследование
- Read разных участков одного большого файла — нормально

**Снижение ложных срабатываний:**
- Не считать Read сразу после Edit того же файла
- Порог = 3 (не 2): 3 одинаковых вызова подряд — почти наверняка зацикливание
- Окно = 20 tool calls (не весь лог): далёкие повторения нормальны

**Плюсы:**
- Может сэкономить $0.50-5.00 за инцидент
- Работает автоматически
- `send_hint` часто помогает Claude выйти из цикла без потери контекста

**Минусы:**
- Ложные срабатывания раздражают
- `restart` — грубое решение, теряет прогресс
- Не ловит "семантическое зацикливание" (разные tool calls, но одинаковый смысл)
- Требует тюнинга threshold и window size под конкретный workflow

**Рекомендация:** `loop_detection = true`, `loop_threshold = 3`, `loop_action = "warn"` (начать с предупреждений, потом перейти на `send_hint` если зацикливания частые).

**Конфигурация:**
```toml
[optimization]
loop_detection = true
loop_threshold = 3
loop_action = "warn"             # warn | send_hint | restart
loop_hint = "You seem to be repeating the same actions. Try a different approach."
loop_window = 20                 # Размер ring buffer (последние N tool calls)
loop_ignore_read_after_edit = true  # Не считать Read сразу после Edit того же файла
```

---

#### Стратегия 9: Сводный дашборд экономии

**Суть:** визуальный дашборд, показывающий сколько потрачено, сколько сэкономлено, и где ещё можно сэкономить. Сам по себе не экономит — но делает расходы видимыми, что мотивирует оптимизацию и помогает принимать решения.

**Что показывает:**

| Метрика | Зачем |
|---|---|
| Total spend today/week/month | Контроль бюджета |
| Cost by model breakdown | Видеть, где тратятся Opus-деньги на Sonnet-задачи |
| Cache efficiency per session | Выявить сессии с плохим кэшированием |
| Context utilization per session | Увидеть кто близок к overflow |
| Saved by caching | Мотивация: "кэш сэкономил $28.50 сегодня" |
| Saved by auto-restart | Показать эффект Стратегии 1 |
| Recommendations | Конкретные действия: "P2 можно перевести на Haiku" |
| Rate limit utilization | Приближение к лимиту подписки |

**Плюсы:**
- Делает оптимизацию измеримой (нельзя оптимизировать то, что не измеряешь)
- Рекомендации помогают пользователю настроить другие стратегии
- Мотивирует: видеть "$28 сэкономлено" приятно

**Минусы:**
- Не экономит сам по себе — только показывает
- Требует хранения метрик в SQLite (доп. место и CPU на запись)
- Может перегрузить UI если показывать слишком много

**Рекомендация:** включить. Не перегружать — основные метрики в StatusBar (cost today, cache efficiency), детали — в отдельном экране CostDashboard.

---

### 20.3.10 Сводная таблица стратегий

| # | Стратегия | Экономия | Сложность | Риски | Автоматическая? | Рекомендация |
|---|---|---|---|---|---|---|
| 1 | Context auto-restart | 20-30% на длинных сессиях | Средняя | Потеря контекста при `fresh` | Да | **Включить** (mode=resume) |
| 2 | Shared system prompt | $1-2/день при 4+ сессиях | Низкая | Нет | Да | **Включить всегда** |
| 3 | Cache warming | ~$0.04 × N на каждый запуск | Низкая | Задержка запуска | Да | **Включить** (delay=3s) |
| 4 | Model routing | До 20x разницы в стоимости | Средняя | Неверный выбор модели | С pre-flight | **Ручной сначала** |
| 5 | Max turns | Защита от $10+ потерь | Низкая | Прерывание задачи | Да | **Включить** (50 default) |
| 6 | Cache monitoring | Косвенная (видимость) | Низкая | Ложные алерты | Да | **Включить** |
| 7 | Effort level | 30-60% на output | Низкая | Ошибки при low effort | Ручная | **medium default** |
| 8 | Loop detection | $0.50-5.00 за инцидент | Средняя | Ложные срабатывания | Да | **Включить** (warn) |
| 9 | Cost dashboard | Косвенная (решения) | Средняя | Нет | UI | **Включить** |

### 20.3.11 Комбинированный эффект

Все стратегии работают вместе. Пример типичного рабочего дня:

```
Без оптимизации (4 Sonnet сессии, ~40 turns каждая, 10 рестартов):
  160 turns × ~$0.04/turn avg = $6.40
  System prompt: 40 × $0.056 = $2.24
  Зацикливание (1 случай, 20 turns): $0.80
  Итого: ~$9.44

С полной оптимизацией:
  Context restart (экономия 25%): $6.40 → $4.80          saved: $1.60
  Shared system prompt: $2.24 → $0.26                     saved: $1.98
  Cache warming: ещё ~$0.10                                saved: $0.10
  Loop detection (предотвращён 1 цикл): $0.80 → $0.12    saved: $0.68
  Model routing (2 задачи на Haiku): ещё ~$0.60           saved: $0.60
  Effort (medium вместо high для 6 из 10 задач): ~$0.50   saved: $0.50
  Итого: ~$4.00

  Экономия: $5.44/день (58%)
  За месяц (22 рабочих дня): ~$120 экономии
```

### 20.4 Конфигурация оптимизации (полная)

```toml
[optimization]
# Context management
context_restart_threshold = 0.75     # Авто-рестарт при контексте > 75%
context_warn_threshold = 0.60        # Предупреждение при > 60%
context_restart_mode = "resume"      # resume | fresh | ask | off

# Cache optimization
exclude_dynamic_system_prompt = true # --exclude-dynamic-system-prompt-sections
session_start_delay = 3              # Секунд между запуском (cache warming)

# Turn limits
max_turns_default = 50               # Дефолтный лимит turns
max_turns_auto_adjust = true         # Автокоррекция на основе истории

# Loop detection
loop_detection = true
loop_threshold = 3                   # Повторов до детекции
loop_action = "warn"                 # warn | send_hint | restart
loop_hint = "You seem to be repeating actions. Try a different approach."

# Auto model routing (используется с pre-flight analysis)
auto_model_routing = false           # true = выбор модели по сложности задачи

# Reporting
show_cost_per_turn = true            # Показывать стоимость каждого turn в логе
show_cache_efficiency = true         # Показывать cache efficiency в session header
```

### 20.5 Автоматические действия менеджера (без участия пользователя)

Даже в полностью автоматическом режиме (`permission_mode = "bypassPermissions"`) менеджер **активно экономит**:

| Действие | Условие | Экономия |
|---|---|---|
| Cache warming delay | При запуске нескольких сессий | ~$0.04 × (N-1) сессий |
| `--exclude-dynamic-system-prompt` | Всегда при >1 сессии | ~$0.04 × (N-1) × кол-во рестартов |
| Авто-рестарт по контексту | context > 75% | ~$0.30/turn после рестарта |
| `--max-turns` | Всегда (защита от зацикливания) | До десятков $ при зацикливании |
| Loop detection + hint | При 3+ повторах tool call | $0.50-5.00 за инцидент |
| `--effort` по типу задачи | Простые задачи = low effort | 30-50% на output tokens |
| Fallback model | Основная перегружена | Избегание retry-задержек |
| Запись cache metrics | Каждый turn | Данные для будущей оптимизации |

### 20.6 Per-turn cost tracking в логе

Каждый turn в логе показывает стоимость:

```
[14:32:15] 💬 Claude: Analysing the auth module...
[14:32:15] 📊 Turn 5: $0.028 │ ctx 34% │ cache 81% │ in:12k out:0.5k
[14:32:18] 🔧 Read: src/auth/provider.go
[14:32:20] 💬 Claude: I see the issue with token validation...
[14:32:20] 📊 Turn 6: $0.031 │ ctx 38% │ cache 79% │ in:14k out:0.8k
[14:32:25] 🔧 Edit: src/auth/provider.go
[14:32:28] 💬 Claude: Fixed the validation logic...
[14:32:28] 📊 Turn 7: $0.035 │ ctx 42% │ cache 76% │ in:16k out:1.2k
```

Строки `📊` опциональны (`show_cost_per_turn = true`). Позволяют отслеживать рост стоимости в реалтайме.

### 20.7 Архитектурные компоненты

```
internal/
  optimization/
    ├── context.go       # Мониторинг контекста, авто-рестарт
    ├── cache.go         # Cache efficiency tracking, warming strategy
    ├── loop.go          # Детекция зацикливания
    ├── routing.go       # Auto model routing по сложности
    └── reporter.go      # Сводный отчёт по экономии
```

## 21. Тестовый и управляющий harness

Цель раздела — описать инфраструктуру, позволяющую **полностью управлять приложением и тестировать все кнопки и параметры без GUI и без расхода API-токенов**. Harness даёт агенту (или CI) тот же набор действий и событий, что доступен UI, плюс детерминированный двойник Claude CLI.

### 21.1 Принципы

1. **UI ≡ набор bind-методов.** Всё, что делает фронтенд, — это вызовы экспортированных методов `SessionManager` и подписка на события `runtime.EventsEmit`. Значит «нажать любую кнопку» = вызвать тот же метод; «увидеть результат» = получить то же событие.
2. **Детерминизм и нулевая стоимость.** Реальный `claude` заменяется на `fakeclaude` — бинарь, эмитящий заранее заданный stream-json. Тесты воспроизводимы и бесплатны.
3. **Один источник событий.** Все события проходят через интерфейс `Emitter`, у которого две реализации: Wails (для UI) и broadcast в control-plane (для агента/CI). Никаких прямых `runtime.EventsEmit` в бизнес-логике.
4. **Покрытие состояний, а не строк.** Сценарии перекрывают весь Session Status Flow (Working / WaitingPermission / RateLimited / Retrying / Error / auto-restart / loop).

### 21.2 Компоненты

```
Claude / CI --MCP--> cm-mcp --RPC/WS--> control-plane --> SessionManager (методы)
     ^                                        ^                    |
     +---------- events (session:*) ----------+            spawns claude_path
                                                                   |
                                                            = fakeclaude (scripted stream-json)
```

| Компонент | Пакет / бинарь | Зависит от приложения |
|---|---|---|
| `fakeclaude` | `cmd/fakeclaude` | нет (только формат stream-json) |
| Сценарии + conformance | `internal/testkit`, `testdata/scenarios` | parser (TASK-03) |
| Control-plane + Emitter | `internal/control` | manager (TASK-06) |
| MCP-сервер | `cmd/cm-mcp` | control-plane |
| E2E scenario-runner | `internal/control` (тесты) | MCP + fakeclaude |
| DOM-харнес | `frontend/tests` | frontend (TASK-07/08) + control-plane |

### 21.3 `fakeclaude` — двойник CLI

Бинарь, ведущий себя как `claude -p --input-format stream-json --output-format stream-json`: читает из stdin сообщения (`user_message`, `permission_response`), пишет в stdout события по сценарию.

**21.3.1 Поведение**

- Игнорирует все CLI-флаги приложения, кроме нужных для идентификации (`--session-id`, `--name`, `--model`) — они доступны для подстановки в события.
- Сценарий выбирается переменной окружения `FAKECLAUDE_SCENARIO`:
  - путь к файлу `.json` → используется он;
  - путь к каталогу → бинарь читает первый `user_message` из stdin и выбирает сценарий, чей `match` (regex) совпал с текстом промпта. Это позволяет одному `fakeclaude` обслуживать много сессий в e2e.
- Шаг `await_stdin` блокирует вывод, пока не придёт сообщение нужного типа, — точно как реальный CLI блокируется на permission и на стартовом `user_message`.
- Задержки (`delay_ms`) эмулируют реалтайм-стриминг; в тестах множитель ускорения через `FAKECLAUDE_SPEED` (по умолчанию 1.0).

**21.3.2 Формат сценария**

```json
{
  "name": "permission-then-result",
  "match": "(?i)refactor auth",
  "model": "claude-sonnet-4-6",
  "context_window": 200000,
  "steps": [
    { "type": "await_stdin", "expect": "user_message", "timeout_ms": 5000 },
    { "type": "emit", "delay_ms": 0,
      "event": { "type": "system", "subtype": "init",
        "session_id": "${session_id}", "model": "${model}",
        "tools": ["Read","Edit","Bash"], "cwd": "${cwd}" } },
    { "type": "emit", "delay_ms": 80,
      "event": { "type": "assistant",
        "message": { "content": [{"type":"text","text":"Reading the auth module..."}],
          "usage": {"input_tokens": 12000, "output_tokens": 400,
                    "cache_read_input_tokens": 9000} } } },
    { "type": "emit", "delay_ms": 60,
      "event": { "type": "assistant",
        "message": { "content": [{"type":"tool_use","id":"t1","name":"Edit",
          "input": {"file_path":"src/auth/provider.go"}}] } } },
    { "type": "emit",
      "event": { "type": "permission_request",
        "request_id": "p1", "tool_name": "Edit",
        "input": {"file_path":"src/auth/provider.go"} } },
    { "type": "await_stdin", "expect": "permission_response",
      "timeout_ms": 30000, "store_as": "perm" },
    { "type": "emit", "delay_ms": 40,
      "event": { "type": "assistant",
        "message": { "content": [{"type":"text","text":"Fixed token validation."}],
          "usage": {"input_tokens": 16000, "output_tokens": 1200,
                    "cache_read_input_tokens": 13000} } } },
    { "type": "emit",
      "event": { "type": "result", "subtype": "success",
        "total_cost_usd": 0.035, "num_turns": 3,
        "usage": {"input_tokens": 40000, "output_tokens": 2100,
                  "cache_read_input_tokens": 31000},
        "modelUsage": {"claude-sonnet-4-6": {"costUSD": 0.035}} } }
  ]
}
```

Подстановки `${session_id}`, `${model}`, `${cwd}`, `${name}` берутся из реальных CLI-флагов, переданных приложением. Реактивный вариант: шаг может содержать `"on": {"perm.decision":"deny"}` — ветвление по сохранённому через `store_as` ответу (например, эмитить `result subtype:"error"` при deny).

**21.3.3 Обязательный набор сценариев** (`testdata/scenarios/`)

| Файл | Состояние / проверка |
|---|---|
| `happy-path.json` | init → assistant×N → result success |
| `permission-allow.json` | permission_request → allow → result |
| `permission-deny.json` | permission_request → deny → ветка отказа |
| `rate-limit.json` | `rate_limit_event` (utilization 0.9, resetsAt) → пауза → продолжение |
| `error-exit.json` | result subtype:"error" + ненулевой exit code |
| `loop.json` | один `tool_use` с идентичным input 3× (триггер LoopDetector) |
| `context-growth.json` | usage растёт до >75% `context_window` (триггер auto-restart) |
| `multi-turn.json` | реакция на несколько `user_message` подряд (bidirectional) |
| `budget-exceeded.json` | result с `total_cost_usd` выше `max_budget_usd` |

### 21.4 Control-plane (headless bridge)

**21.4.1 Активация.** При `CM_CONTROL=1` (или флаге `-control`) приложение поднимает локальный сервер на `127.0.0.1:${CM_CONTROL_PORT:-7333}`. Слушает только loopback. Требует заголовок `X-CM-Token`, значение — из `CM_CONTROL_TOKEN` (генерируется и печатается в stdout при старте, если не задан). GUI при этом работает как обычно — control-plane дополняет, а не заменяет UI.

**21.4.2 Emitter-индирекция.** Вся бизнес-логика эмитит события через интерфейс:

```go
type Emitter interface {
    Emit(event string, data any) // event: "session:status", "session:log", ...
}
```

- `WailsEmitter` — обёртка над `runtime.EventsEmit` (прод/GUI).
- `MultiEmitter` — раздаёт в несколько Emitter-реализаций; в control-режиме = Wails + ControlEmitter.
- `ControlEmitter` — сериализует `{event, data, ts}` и broadcast в WS-клиентов; буферизует последние N событий на сессию для `wait_for_*`.

`SessionManager` принимает `Emitter` в конструкторе. Никаких прямых вызовов `runtime` вне `WailsEmitter`.

**21.4.3 RPC-протокол.** HTTP `POST /rpc`, тело JSON-RPC 2.0:

```json
{ "jsonrpc":"2.0", "id":1, "method":"StartSession",
  "params": {"project":"lumen","session":"P1"} }
```

Методы один-в-один с публичным API `SessionManager` (см. §6.1, §8): `StartSession`, `StopSession`, `StopAll`, `RestartSession`, `ResumeSession`, `SendMessage`, `RespondPermission`, `GetPendingPermissions`, `GetAllSessions`, `GetSessionLog`, `GetHistory`, `GetSessionMetrics`, `GetDailyCost`, `GetProjectCost`, `GetRateLimitStatus`, `StartProject`, `StopProject`, `GetConfig`, `UpdateConfig`, `RunAnalysis`, `ApprovePlan`, `ExecutePlan`. Роутер строится из единого реестра, общего с Wails-биндингами, — добавление метода в манагер автоматически доступно и UI, и control-plane.

**21.4.4 Event stream.** WS `GET /events` — поток всех событий `session:*` в формате `ControlEmitter`. Плюс эндпоинт для блокирующих ожиданий (см. MCP `wait_for_*`): `POST /wait` с `{event, match, timeout_ms}` — отвечает первым событием, удовлетворяющим `match` (или из буфера, если уже наступило), либо таймаутом.

### 21.5 MCP-сервер `claude-manager-mcp`

`cmd/cm-mcp` — stdio MCP-сервер, тонкий прокси в control-plane. Регистрируется в Claude Code: `claude mcp add cm -- cm-mcp` (адрес/токен control-plane через env). Делает действия приложения нативными инструментами агента.

| Инструмент | Параметры | Возвращает |
|---|---|---|
| `start_session` | project, session | status |
| `stop_session` | project, session, [after_task] | ok |
| `restart_session` | project, session, [resume] | ok |
| `send_message` | project, session, text | ok |
| `approve_permission` | request_id, [scope: once\|session\|always] | ok |
| `deny_permission` | request_id, [scope] | ok |
| `get_sessions` | — | список сессий + статусы/метрики |
| `get_session_logs` | project, session, [tail] | строки лога |
| `get_pending_permissions` | — | очередь ожидающих разрешений |
| `get_metrics` | [project], [period] | cost/tokens/cache |
| `set_global_settings` | partial config | новый config |
| `run_preflight` | project, task | план анализа |
| `execute_plan` | plan_id | ok |
| `wait_for_status` | project, session, status, [timeout_ms] | финальный статус |
| `wait_for_event` | event, [match], [timeout_ms] | событие |

**`wait_for_*` — ключевой примитив.** Реализован через `POST /wait`: подписывается на поток и/или сверяется с буфером. Делает асинхронные проверки надёжными: «запусти → дождись WaitingPermission → одобри → дождись Idle». Без него тесты флакают на гонках.

### 21.6 Сквозной scenario-runner (e2e)

**21.6.1 Формат e2e-сценария** (`testdata/e2e/*.json`) — список шагов «действие → ожидание»:

```json
{
  "name": "permission-flow",
  "fakeclaude_dir": "testdata/scenarios",
  "config": "testdata/configs/one-session.toml",
  "steps": [
    { "do": "StartSession", "with": {"project":"lumen","session":"P1"} },
    { "wait": "session:status", "match": {"id":"lumen/P1","status":"WaitingPermission"} },
    { "do": "RespondPermission", "with": {"request_id":"p1","decision":"allow"} },
    { "wait": "session:status", "match": {"id":"lumen/P1","status":"Idle"} },
    { "assert": "GetSessionMetrics", "with": {"id":"lumen/P1"},
      "expect": {"cost_usd": 0.035, "turns": 3} }
  ]
}
```

**21.6.2 Запуск под тестом.** Go-тест поднимает приложение в `CM_CONTROL=1` с `claude_path=$(which fakeclaude)` и `FAKECLAUDE_SCENARIO=testdata/scenarios`, проигрывает каждый e2e-сценарий через RPC/WS, проверяет `assert`. Один и тот же файл сценария используется и тестом, и интерактивно агентом через MCP.

### 21.7 Frontend DOM-харнес (Playwright)

Слои §21.3–21.6 покрывают бэкенд-логику. Для проверки реальных кнопок:

- `frontend/playwright.config.ts` + `frontend/tests/*.spec.ts`.
- Фронт поднимается через vite dev-server (или `wails dev`), бэкенд — в control-режиме с `fakeclaude`.
- Тесты кликают реальный DOM: start в `Sidebar`, ввод в `SessionInput`, Allow/Deny в `PermissionBanner`, вкладки в `Settings`, строки в `History`.
- Проверка — двойная: состояние DOM + соответствующее событие в WS control-plane. Это ловит рассинхрон «кнопка нажата, но метод не вызван».

### 21.8 Структура каталогов harness

```
cmd/
  fakeclaude/          # двойник CLI (§21.3)
  cm-mcp/              # MCP-сервер (§21.5)
internal/
  testkit/
    scenario.go        # структура Scenario, загрузчик, подстановки
    conformance_test.go  # fakeclaude-вывод <-> session/parser
  control/
    server.go          # WS+HTTP, CM_CONTROL, токен
    rpc.go             # JSON-RPC роутер на методы манагера
    emit.go            # Emitter, WailsEmitter, MultiEmitter, ControlEmitter
    wait.go            # /wait, буфер событий, wait_for_*
    mcptools.go        # определения MCP-инструментов
    e2e_test.go        # сквозной scenario-runner (§21.6)
testdata/
  scenarios/           # сценарии fakeclaude (§21.3.3)
  e2e/                 # сквозные сценарии (§21.6.1)
  configs/             # тест-конфиги TOML
frontend/
  playwright.config.ts
  tests/               # DOM-спеки (§21.7)
```

### 21.9 Связь с TASKS

Детальная разбивка harness на сессионные задачи — в [HARNESS-TASKS.md](./HARNESS-TASKS.md) (H1–H6). Условие сцепки: `SessionManager` (TASK-06) должен с самого начала эмитить события через `Emitter` (§21.4.2), иначе потребуется рефакторинг. Это требование добавлено в промпт TASK-06.
