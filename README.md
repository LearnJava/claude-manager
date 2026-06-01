# Claude Session Manager

Десктопное приложение для Windows, позволяющее запускать, отслеживать и управлять несколькими параллельными сессиями [Claude Code CLI](https://claude.ai/code) одновременно — в разных проектах, с разными моделями и настройками.

---

## Содержание

- [Требования](#требования)
- [Установка зависимостей](#установка-зависимостей)
- [Конфигурация](#конфигурация)
- [Разработка (dev-режим)](#разработка-dev-режим)
- [Сборка релизного билда](#сборка-релизного-билда)
- [Запуск](#запуск)
- [Возможности](#возможности)
- [Авто-выбор модели (Model Routing)](#авто-выбор-модели-model-routing)
- [Тестирование](#тестирование)
- [Структура проекта](#структура-проекта)
- [Часто задаваемые вопросы](#часто-задаваемые-вопросы)

---

## Требования

| Инструмент | Версия | Как проверить |
|---|---|---|
| Go | 1.23+ | `go version` |
| Node.js | 18+ | `node --version` |
| npm | 9+ | `npm --version` |
| Wails CLI | v2 | `wails version` |
| Claude Code CLI | последняя | `claude --version` |
| GCC (для CGo/SQLite) | любая | `gcc --version` |

**Установка Wails CLI:**
```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

**GCC на Windows** — проще всего через [TDM-GCC](https://jmeubank.github.io/tdm-gcc/) или `winlibs`. Нужен для компиляции `go-sqlite3`.

---

## Установка зависимостей

```bash
# Go-зависимости
go mod download

# Node-зависимости фронтенда
cd frontend && npm install && cd ..
```

---

## Конфигурация

Приложение читает конфиг из `~/.claude-manager/config.toml` (создаётся автоматически при первом запуске с дефолтами).

Для быстрого старта скопируйте пример:

```bash
mkdir %USERPROFILE%\.claude-manager
copy config.example.toml %USERPROFILE%\.claude-manager\config.toml
```

Минимальный рабочий конфиг:

```toml
[settings]
claude_path = "claude"   # путь к CLI; если claude не в PATH — укажите полный

[[project]]
name = "my-project"
path = 'D:\Projects\my-project'

  [[project.session]]
  name = "S1"
  prompt = "Work on the next task from TODO.md."
  model = "sonnet"
  permission_mode = "acceptEdits"
```

Конфиг также редактируется прямо в интерфейсе через **Settings** (три вкладки: Global / Projects / Sessions).

### Ключевые параметры конфига

| Секция | Параметр | Описание |
|---|---|---|
| `[settings]` | `claude_path` | Путь к Claude CLI (по умолчанию `claude`) |
| `[settings]` | `theme` | Тема: `dark` или `light` |
| `[settings]` | `log_retention_days` | Хранить логи N дней (0 = вечно) |
| `[settings]` | `session_start_delay` | Задержка между запусками сессий (cache warming) |
| `[optimization]` | `auto_model_routing` | Авто-выбор модели по сложности задачи |
| `[optimization]` | `context_restart_threshold` | Авто-рестарт при заполнении контекста (0.75 = 75%) |
| `[optimization]` | `loop_detection` | Детектор зацикливания (повторные tool-вызовы) |
| `[[project.session]]` | `model` | `haiku` / `sonnet` / `opus` или полный ID модели |
| `[[project.session]]` | `effort` | `low` / `medium` / `high` / `xhigh` / `max` |
| `[[project.session]]` | `permission_mode` | `default` / `acceptEdits` / `auto` / `bypassPermissions` |
| `[[project.session]]` | `max_budget_usd` | Лимит расхода на сессию (0 = без лимита) |

---

## Разработка (dev-режим)

Dev-режим запускает Go-бэкенд и Vite-сервер фронтенда одновременно с горячей перезагрузкой UI.

```bash
wails dev
```

- Окно приложения откроется автоматически.
- Изменения в `.svelte` / `.ts` файлах применяются без перезапуска.
- Изменения в Go-коде требуют перезапуска `wails dev`.

---

## Сборка релизного билда

```bash
wails build
```

Готовый исполняемый файл появится в:
```
build\bin\claude-manager.exe
```

Размер бинарника ~15–20 МБ (фронтенд встроен через `//go:embed`).

Для сборки без отладочной консоли:
```bash
wails build -windowsconsole=false
```

---

## Запуск

### Из релизного билда
```
build\bin\claude-manager.exe
```

### При первом запуске
1. Нажмите **+** в шапке панели Projects (или **Settings → Projects → Add project**).
2. Укажите имя и путь к проекту, добавьте сессию на вкладке **Sessions**.
3. Нажмите **Save**, затем `▶` рядом с сессией — она запустится.
4. Логи появятся в режиме реального времени в основной панели.

### Управление проектами из боковой панели

| Действие | Как |
|---|---|
| Добавить проект | Кнопка **+** в шапке панели Projects |
| Удалить проект | Навести → кнопка **✕** (первый клик: предупреждение **?**, второй: удаление) |
| Запустить все сессии проекта | Навести на проект → **▶** |
| Остановить все | Навести на проект → **■** |
| Запустить / остановить сессию | Навести на сессию → **▶** / **■** |

---

## Возможности

### Параллельные сессии
Запускайте несколько Claude-сессий одновременно в разных проектах. Каждая — отдельный процесс с собственным контекстом, моделью и настройками прав.

### Управление разрешениями
Когда Claude запрашивает доступ к файлу или команде:
- Запрос появляется как баннер прямо в окне сессии
- Все ожидающие запросы видны в **Permission Queue** (кнопка в статус-баре)
- Можно настроить авто-одобрение по шаблонам в **Settings → Sessions → Auto-approve rules**

### История и стоимость
- **History** — таблица всех прошлых запусков с фильтрацией и экспортом CSV
- **Dashboard** — стоимость по дням/проектам, эффективность кэша, статус rate limit

### Экспорт логов
Кнопка **Export** в панели сессии сохраняет лог в формате MD, JSON или TXT через нативный диалог.

### Keyboard shortcuts

| Сочетание | Действие |
|---|---|
| `Ctrl+1` … `Ctrl+4` | Переключение между первыми четырьмя сессиями |
| `Ctrl+F` | Поиск по логу текущей сессии |
| `Escape` | Закрыть открытый диалог |

---

## Авто-выбор модели (Model Routing)

Функция позволяет автоматически выбирать модель Claude на основе сложности задачи, определяемой через pre-flight анализ. Это может сэкономить до 20x стоимости (Haiku vs Opus).

### Таблица маршрутизации

| Сложность | Модель | Effort | Примеры задач |
|---|---|---|---|
| Trivial | haiku | low | Форматирование, переименование, bumps версий |
| Standard | sonnet | medium | Баг-фиксы, новые endpoints, тесты |
| Complex | sonnet | high | Многофайловые изменения, оптимизация |
| Architectural | opus | high | Новые подсистемы, миграции БД |

### Включение

В **Settings → Global → Model routing** включите чекбокс **Auto model routing**, затем нажмите **Save**.

Или в конфиге:
```toml
[optimization]
auto_model_routing = true
```

### Как работает при запуске сессии

1. Пользователь нажимает `▶` на сессии.
2. Приложение запускает lightweight pre-flight анализ (модель haiku, несколько секунд).
3. Открывается окно **ModelPicker** с результатом:
   - Сложность задачи и причина выбора
   - Рекомендованные модель и effort
4. Пользователь выбирает:
   - **Start with sonnet/medium** — принять рекомендацию
   - **Override model / effort** — развернуть дропдауны и выбрать вручную
   - **Cancel** — отменить запуск

### Отключение роутинга для конкретной сессии

Снимите галочку в Settings или переключите `auto_model_routing = false` в конфиге — при следующем запуске сессия стартует немедленно с моделью из конфига.

---

## Тестирование

### Unit-тесты Go (все пакеты)

```bash
go test ./...
```

Покрытые пакеты:

| Пакет | Что тестируется |
|---|---|
| `internal/session` | Парсер stream-json, менеджер сессий, lifecycle |
| `internal/permission` | Правила авто-разрешений, очередь, обработчик |
| `internal/analysis` | Preflight-анализ, планы задач |
| `internal/optimization` | Контекстный монитор, кэш, детектор петель, роутер модели |
| `internal/hooks` | Выполнение pre/post-task хуков |

### С детализацией

```bash
go test ./... -v
```

### Отдельный пакет

```bash
go test ./internal/optimization/... -v
go test ./internal/session/... -v
```

### С отчётом покрытия

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Typecheck фронтенда

```bash
cd frontend
npm run check
```

---

## Структура проекта

```
claude-manager/
├── main.go                    # точка входа Wails
├── app.go                     # Go-методы, доступные из JS (26+ биндингов)
├── config.example.toml        # пример конфига со всеми параметрами
├── internal/
│   ├── config/                # загрузка/сохранение TOML, дефолты, валидация
│   ├── session/
│   │   ├── manager.go         # SessionManager: старт/стоп/override/события
│   │   ├── session.go         # Session goroutine: bidirectional streaming
│   │   ├── parser.go          # Парсер stream-json событий CLI
│   │   ├── input.go           # Запись в stdin (user_message, permission_response)
│   │   └── ratelimit.go       # Детекция rate limit, retry-логика
│   ├── permission/            # авто-разрешения, UI-очередь, glob-правила
│   ├── analysis/              # preflight-анализ, планы задач, JSON Schema
│   ├── optimization/
│   │   ├── routing.go         # Авто-выбор модели по сложности задачи
│   │   ├── context.go         # Монитор контекста, авто-рестарт по порогу
│   │   ├── cache.go           # Cache efficiency tracking, stagger delay
│   │   └── loop.go            # Детектор зацикливания (ring buffer)
│   ├── store/                 # SQLite: runs, logs, metrics, plans
│   └── hooks/                 # pre/post-task хуки (shell команды)
└── frontend/src/
    ├── App.svelte              # корневой компонент, shortcuts, модальные окна
    ├── components/
    │   ├── Sidebar.svelte      # дерево проектов/сессий, старт/стоп/удаление
    │   ├── ModelPicker.svelte  # выбор модели при авто-роутинге (с override)
    │   ├── SessionView.svelte  # основная панель: лог, управление, экспорт
    │   ├── LogStream.svelte    # реалтайм-лог с поиском и автоскроллом
    │   ├── PermissionBanner.svelte  # инлайн-баннер запроса разрешения
    │   ├── PermissionQueue.svelte   # очередь всех ожидающих разрешений
    │   ├── Settings.svelte     # настройки: Global / Projects / Sessions
    │   ├── History.svelte      # история запусков, фильтрация, CSV-экспорт
    │   ├── CostDashboard.svelte # стоимость, кэш-эффективность, rate limit
    │   └── StatusBar.svelte    # нижняя строка: счётчики, rate limit
    └── stores/                 # Svelte-сторы (сессии, проекты, тема, поиск)
```

---

## Часто задаваемые вопросы

**`gcc` not found при сборке**
Установите TDM-GCC и убедитесь, что `gcc` есть в `PATH`.

**`wails` not found**
Убедитесь, что `%USERPROFILE%\go\bin` добавлен в `PATH`.

**Сессия зависает в статусе WaitingPermission**
Claude запросил разрешение — найдите его в баннере сессии или в Permission Queue и нажмите Allow / Deny.

**Конфиг не применяется после Save**
Убедитесь, что нажали **Save** (не просто закрыли Settings) — изменения записываются в TOML только явно.

**ModelPicker не открывается при нажатии ▶**
Авто-роутинг отключён. Включите **Settings → Global → Auto model routing** и сохраните.

**Pre-flight анализ занимает слишком долго**
Это нормально — haiku делает быстрый анализ (5–15 сек). Если мешает, отключите `auto_model_routing`.

**Проект удалился случайно**
Откройте `~/.claude-manager/config.toml` в текстовом редакторе — последняя сохранённая версия там.
