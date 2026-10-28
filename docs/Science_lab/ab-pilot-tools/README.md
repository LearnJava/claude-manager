# Инструменты анализа A/B-пилота 28.10.2026

Скрипты только читают: транскрипты Claude Code (`~/.claude/projects`, живут около 30 дней, то есть до ~27.11.2026), лог
менеджера (`~/.claude-manager/logs/app.log`), `~/.claude-manager/history.db`, стенд `C:\kostja\GoProjects\cm-bench`.
Запуск: python по полному пути, флаг `-I`, `PYTHONIOENCODING=utf-8`. Путь стенда и имя проекта заданы аргументами со значениями
пилота по умолчанию (см. `docs/change-acceptance.md` §2); без аргументов вывод побайтно прежний.

| Скрипт | Что считает | Аргументы |
|---|---|---|
| `analyze_speed.py` | по задачам: время агента (инструменты / модель), «тяжёлые» вызовы >60 с, ходы, ворота B; `A:T5 B:T5.3` — самые долгие вызовы | `--bench-dir`, `--project`, окна `A:T5 B:T5.3` |
| `calls.py` | классы вызовов (git, файлы очереди и слота, тесты и сборки, скиллы), вызовы до первой записи и после последней | `--bench-dir`, `--project` |
| `cost.py` | $ по категориям (запись кэша, чтение, вывод) и вызовы по инструментам; читает `../measurements/2026-10-28-ab-pilot/bench.json` | `--bench-json` (или путь первым аргументом) |
| `latency.py` | задержка шага: вызов инструмента и пауза модели, A против B | `--bench-dir`, `--project` |
| `audit.py` | начальный контекст сессии, блокировки хуков, неопознанные (фоновые) сессии B | `--bench-dir`, `--project`, `--clone-b` |
| `slow.py` | самые медленные пакеты и тесты: `go test -count=1 -json ./... > gotest.json; python -I slow.py gotest.json` | путь к `gotest.json` |

Аргументы: `--bench-dir` (по умолчанию `C:\kostja\GoProjects\cm-bench`, из него читается `_logs/A-*.jsonl`), `--project` (`bench-cm`, имя проекта в логе менеджера), `--clone-b` (по умолчанию `<bench-dir>\b`; по нему ищется папка транскриптов B). Клон A отдельного аргумента не требует: сессии A находятся по `_logs/` стенда.
Пример повтора на клонах `a2`/`b2` и проекте `bench-cm2`:

```
python -I analyze_speed.py --bench-dir C:\kostja\GoProjects\cm-bench2 --project bench-cm2 A:T5 B:T5.3
python -I audit.py --bench-dir C:\kostja\GoProjects\cm-bench2 --project bench-cm2 --clone-b C:\kostja\GoProjects\cm-bench2\b2
python -I cost.py --bench-json путь\к\bench2.json
```

Время набора тестов на старых коммитах (прогрев компиляции не в счёт): для каждого коммита `git clone` в отдельную папку,
`git checkout <sha>`, `mkdir -p frontend` и копия `frontend/dist` (нужна `go:embed` корневого пакета), затем
`go test -count=1 -run NO_SUCH ./...` (прогрев) и `time go test -count=1 ./...`. Пилот: 26.09 `bf278a2` 111 с, 14.10
`c03c875` 156 с, 24.10 `9470fa8` 222 с, 28.10 `001193b` 444 с.

Данные пилота: `../measurements/2026-10-28-ab-pilot/`: `bench.json` (вывод `cm-bench/_kit/bench_measure.py --json`),
`RESULTS.md` (итог стенда), `events.jsonl` (журнал стенда).
