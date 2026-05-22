# Оркестратор задач Claude Manager

Автоматический запуск Claude Code сессий для последовательного
выполнения задач из TASKS.md. Каждая задача — отдельная сессия
с чистым контекстом. Зависимости между задачами учитываются автоматически.

## Требования

- Python 3.10+
- Claude Code CLI (`claude`) в PATH
- Файл `TASKS.md` в корне проекта

## Запуск

```bash
# С первой доступной задачи — до конца
python scripts/orchestrator.py

# Максимум 3 задачи, потом стоп
python scripts/orchestrator.py --max-tasks 3

# Начать с конкретной задачи (зависимости должны быть выполнены)
python scripts/orchestrator.py --task TASK-05
```

## Статус

```bash
python scripts/orchestrator.py --status
```

Пример вывода:

```
Статус задач Claude Manager:
============================================================

  Выполнено:
    TASK-01: Project scaffold + config system
           3м 42с, $0.0891, 2026-05-22 14:35:10
    TASK-02: SQLite store + migrations
           2м 15с, $0.0543, 2026-05-22 14:38:30

  В работе:
    TASK-03: Stream-JSON parser

  Готово к запуску:
    TASK-05: Permission system

  Заблокировано (ждут зависимости):
    TASK-04: Session core
           <- TASK-03
    TASK-06: Session Manager + Wails bindings
           <- TASK-02, TASK-04, TASK-05

============================================================
Прогресс: 2/15 (13%)  |  Потрачено: $0.1434
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

# Сбросить все задачи (начать с нуля)
python scripts/orchestrator.py --reset-all
```

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
    ├─► Проверка .stop → найден? → СТОП
    │
    ├─► Поиск следующей задачи (зависимости выполнены?) → нет? → СТОП
    │
    ├─► .jobstatus ← «работает | TASK-XX: Title»
    │
    ├─► Запуск: claude -p "prompt" --dangerously-skip-permissions
    │       Claude читает CLAUDE.md и PLAN.md
    │       Выполняет задачу
    │       Сессия завершается
    │
    ├─► Проверка кода возврата
    │       0 → задача выполнена, записать в .taskstate.json
    │       ≠0 + rate limit → пауза 5 мин, повтор
    │       ≠0 → пауза 30 сек, повтор (до 3 попыток)
    │
    └─► Следующая итерация цикла
```

## Обработка ошибок

- **Rate limit**: пауза 5 минут, потом повтор
- **Ошибка Claude**: пауза 30 секунд, повтор (до 3 раз)
- **3 провала подряд**: задача пропускается, переход к следующей
- **claude не в PATH**: остановка оркестратора

## Служебные файлы

| Файл | Назначение |
|---|---|
| `scripts/.taskstate.json` | Состояние задач (выполнено/в работе/ошибки) |
| `scripts/.stop` | Стоп-сигнал (создаётся `--stop`, удаляется оркестратором) |
| `scripts/.jobstatus` | Текущий статус оркестратора |

Все служебные файлы начинаются с точки — добавьте в `.gitignore`:

```
scripts/.taskstate.json
scripts/.stop
scripts/.jobstatus
```
