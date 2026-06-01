# Оркестратор задач Claude Manager

Автоматический запуск Claude Code сессий для последовательного
выполнения задач из TASKS.md. Каждая задача — отдельная сессия
с чистым контекстом. Зависимости между задачами учитываются автоматически.

## Требования

- Python 3.10+
- Claude Code CLI (`claude`) в PATH
- Файл `TASKS.md` в корне проекта (обязателен)
- Файл `HARNESS-TASKS.md` в корне проекта (опционален, подхватывается автоматически)

## Запуск

```bash
# С первой доступной задачи — до конца
python scripts/orchestrator.py

# Максимум 3 задачи, потом стоп
python scripts/orchestrator.py --max-tasks 3

# Начать с конкретной задачи (зависимости должны быть выполнены)
python scripts/orchestrator.py --task TASK-05

# Начать с конкретной харнес-задачи
python scripts/orchestrator.py --task HARNESS-01

# Стартовать сразу на Haiku (быстрее и дешевле)
python scripts/orchestrator.py --model haiku

# Sonnet по умолчанию, при rate limit — автоматически на Haiku без паузы
python scripts/orchestrator.py --fallback-model haiku

# Комбинация: стартуем на Sonnet, резерв Haiku (unattended)
python scripts/orchestrator.py --model sonnet --fallback-model haiku

# Стартовать с нуля без попытки --resume прерванной сессии
python scripts/orchestrator.py --new
```

**Алиасы моделей** (`--model`, `--fallback-model`, env-переменные):

| Alias    | Полный model ID       |
|----------|-----------------------|
| `haiku`  | `claude-haiku-4-5`    |
| `sonnet` | `claude-sonnet-4-6`   |
| `opus`   | `claude-opus-4-7`     |

Можно указать и полный ID напрямую (любая модель Claude).

**Env-переменные** (удобно для автозапуска):

```bash
CM_MODEL=haiku python scripts/orchestrator.py
CM_FALLBACK_MODEL=haiku python scripts/orchestrator.py
```

## Статус

```bash
python scripts/orchestrator.py --status
```

Пример вывода:

```
Статус задач Claude Manager:
============================================================

────────────────────────────────────────────────────────────
  App Tasks (TASK-XX)
────────────────────────────────────────────────────────────

  Выполнено:
    TASK-01: Project scaffold + config system
           3м 42с, $0.0891, 2026-05-22 14:35:10
    TASK-02: SQLite store + migrations
           2м 15с, $0.0543, 2026-05-22 14:38:30

  В работе:
    TASK-03: Stream-JSON parser
           session_id: abc12345def67890...

  Готово к запуску:
    TASK-05: Permission system

  Заблокировано (ждут зависимости):
    TASK-04: Session core — ...
           <- TASK-03

  2/15 (13%)  |  $0.1434

────────────────────────────────────────────────────────────
  Harness Tasks (HARNESS-XX)
────────────────────────────────────────────────────────────

  Готово к запуску:
    HARNESS-01: `fakeclaude` — двойник CLI + формат сценариев

  Заблокировано (ждут зависимости):
    HARNESS-02: Библиотека сценариев на все состояния + conformance
           <- HARNESS-01

  0/6 (0%)  |  $0.0000

============================================================
Итого: 2/21 (9%)  |  Потрачено: $0.1434
Оркестратор: работает
```

## Остановка

```bash
# Мягкая остановка — после текущей задачи
python scripts/orchestrator.py --stop

# Немедленная остановка — Ctrl+C в окне
```

### Как работает мягкая остановка

1. `--stop` создаёт файл `scripts/.stop`
2. Цикл проверяет его **между задачами**
3. Текущая задача всегда доработает до конца
4. После завершения цикл увидит стоп-файл, удалит его и остановится

## Сброс

```bash
# Сбросить одну задачу (можно перезапустить)
python scripts/orchestrator.py --reset TASK-05
python scripts/orchestrator.py --reset HARNESS-01

# Сбросить все задачи (начать с нуля)
python scripts/orchestrator.py --reset-all
```

## Обработка ошибок и rate limit

### Rate limit — fallback на резервную модель

При первом rate limit оркестратор **не** ставит паузу 5 минут.
Вместо этого показывает интерактивное меню:

```
============================================================
Выберите резервную модель для переключения:
  1) haiku    — самая быстрая, отдельные щедрые лимиты (рекомендуется)
  2) sonnet   — баланс скорости и качества
  3) opus     — мощная, обычно общие лимиты с Sonnet
  4) Ввести имя модели вручную
============================================================
  (для unattended-режима задайте --fallback-model или CM_FALLBACK_MODEL)
Выбор [1 = haiku]:
```

После выбора — продолжение без паузы на резервной модели.
Если и резервная модель исчерпана — стандартная пауза 5 минут.
Сброс fallback: только перезапуском оркестратора.

Для unattended-режима (ночные сессии, CI): `--fallback-model haiku`.

### Auth error (403)

Пауза 60 секунд, файл состояния сессии удаляется, попытка повторяется.

### Прочие ошибки

Пауза 30 секунд, повтор (до 3 попыток). После 3 провалов задача пропускается.

## Восстановление после краша

При старте каждой задачи оркестратор пишет `scripts/.session.json`:

```json
{
  "task_id": "TASK-03",
  "started_at": "2026-05-22T14:38:30",
  "session_id": "abc123..."
}
```

`session_id` захватывается из первого stream-json события сразу.
При нормальном завершении файл удаляется.

Если оркестратор упал (Ctrl+C, закрытие окна, питание):
- При следующем запуске найдёт `.session.json`
- Если есть `session_id` → запустит `claude --resume <id>` с recovery-промптом
- Claude увидит историю диалога и продолжит с места остановки
- Если `session_id` нет → сбросит файл, начнёт задачу заново

### Принудительный старт с нуля (`--new`)

```bash
python scripts/orchestrator.py --new
```

Удаляет `.session.json` и запускает без `--resume`.
Удобно когда прошлая сессия зависла или её контекст устарел.

## Зависимости задач

Оркестратор автоматически парсит граф зависимостей из TASKS.md.
Задача запускается только когда все её зависимости выполнены.

```
TASK-01 (scaffold)
├── TASK-02 (SQLite)
├── TASK-03 (parser)
│   ├── TASK-04 (session core)
│   └── TASK-05 (permissions)
│        └── TASK-06 (manager) ← также ждёт TASK-02, TASK-04
│              ├── TASK-07 (frontend layout)
│              │   ├── TASK-08, TASK-09, TASK-10, TASK-11
│              │   └── TASK-14 (← также TASK-13)
│              ├── TASK-12 (optimization)
│              ├── TASK-13 (preflight backend)
│              └── TASK-15 (hooks) ← также ждёт TASK-08
```

## Жизненный цикл задачи

```
orchestrator.py запускается
    │
    ├─► --new задан? → удалить .session.json
    │
    ├─► Иначе: проверка .session.json → есть session_id?
    │       Да → возобновление через `claude --resume`
    │       Нет → сбросить файл, стартовать заново
    │
    ├─► Проверка .stop → найден? → СТОП
    │
    ├─► Поиск следующей задачи (зависимости выполнены?) → нет? → СТОП
    │
    ├─► .jobstatus ← «работает | TASK-XX: Title»
    ├─► .session.json ← {task_id, started_at}
    │
    ├─► Запуск: claude -p "..." --dangerously-skip-permissions [--model <id>]
    │       session_id захватывается → дописывается в .session.json
    │       Claude читает CLAUDE.md и PLAN.md
    │       Выполняет задачу
    │       Сессия завершается
    │
    ├─► Проверка кода возврата
    │       0          → задача выполнена → .session.json удалён
    │       rate limit → fallback на резервную модель ИЛИ пауза 5 мин
    │       auth 403   → пауза 60 сек, повтор
    │       прочее ≠0  → пауза 30 сек, повтор (до 3 раз)
    │
    └─► Следующая итерация
```

## Служебные файлы

| Файл | Назначение |
|---|---|
| `scripts/.taskstate.json` | Состояние задач (выполнено/в работе/ошибки) |
| `scripts/.session.json` | Состояние активной сессии (task_id + session_id для --resume) |
| `scripts/.stop` | Стоп-сигнал (создаётся `--stop`, удаляется оркестратором) |
| `scripts/.jobstatus` | Текущий статус оркестратора |

Все служебные файлы начинаются с точки — добавлены в `.gitignore`.
