# RUNBOOK: A/B-бенчмарк GLM — ZCode vs Claude Code (через claude-manager)

Цель: прогнать задачу `t6-polyfix` (14 независимых правок, ожидаемо 30–45 вызовов
инструментов на прогон) на обоих «плечах» и сравнить: скорость, расход токенов,
ошибки, течение работы. Всё уже построено и отлажено — нужно только запустить и
корректно собрать результат.

## 1. Что сравниваем

Оба плеча ходят в один и тот же бэкенд `api.z.ai/api/anthropic` (Anthropic-совместимый
фасад, ключ GLM Coding Plan), модель GLM-5.3, effort high. Разница — агентная обвязка:

| | Плечо `cm` | Плечо `zcode` |
|---|---|---|
| Агент | Claude Code CLI (`claude -p --output-format stream-json`) | ZCode (ядро десктоп-приложения, `zcode app-server --stdio`) |
| Кто запускает | claude-manager с провайдером «Claude GLM» (settings-файл + env ANTHROPIC_AUTH_TOKEN) | прямой NDJSON JSON-RPC клиент внутри харнесса |
| Управление | контроль-плоскость менеджера HTTP 127.0.0.1:7333 | stdio-протокол app-server |

## 2. Инфраструктура (уже готова, ничего не писать)

- **Харнесс**: `scripts/bench_glm.py` (в git, master). Один скрипт на оба плеча.
- **Задачи**: `benchmarks/glmlab/tasks/t1..t6/` — каждая: `task.md` (промпт,
  уходит обоим плечам дословно), `starter/` (мини-репо), `hidden/` (скрытые тесты,
  в песочницу не попадают), `verify.py` (оракул: 0=PASS, 1=FAIL; принимает путь
  песочницы первым аргументом).
- **Песочница**: `benchmarks/glmlab/sandbox/` — стабильный каталог-проект
  «GLM Lab» в менеджере; перед каждым прогоном харнесс стирает всё, кроме
  `.claude-manager/config.toml` (там сессия `bench` с `permission_mode =
  bypassPermissions`), копирует `starter/`, делает свежий `git init`.
- **Результаты**: `benchmarks/glmlab/runs/results.csv` + транскрипты
  `runs/<arm>-<task>-r<rep>.json` + журнал `runs/bench.log`.
- **Прото-факты**: `sandbox/` и `runs/` в `.gitignore`; в runs уже лежат 59 строк
  прошлых прогонов (t1–t5, glm-5.3 и glm-5.3-flash) — НЕ удалять, новые допишутся.

## 3. Предусловия

1. Репо: `C:\kostja\GoProjects\claude-manager`, ветка master (всё уже вмержено,
   включая фикс токенов c2f6201/fa6ff8b).
2. Python: всегда полный путь
   `C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe`
   (WindowsApps-алиас медленный).
3. Менеджер: `build/bin/claude-manager.exe` собран из текущего HEAD. Харнесс
   сам запускает его, если не поднят (окно GUI откроется на рабочем столе), и сам
   отбрасывает устаревший `~/.claude-manager/control.json`.
4. API-ключ уже в `~/.claude-manager/config.toml` (`zcode_api_key`) — харнесс
   найдёт сам; ключ нигде не печатать.
5. ZCode установлен: `%LOCALAPPDATA%\Programs\ZCode\` (ZCode.exe + resources/glm/zcode.cjs).

## 4. Как запускать

```bash
cd /c/kostja/GoProjects/claude-manager

# проверка окружения (менеджер, проект GLM Lab, ключ, zcode):
.../python.exe scripts/bench_glm.py --setup

# ЦЕЛЕВОЙ ПРОГОН: t6 на обоих плечах, 2 повтора = 4 прогона:
.../python.exe scripts/bench_glm.py --run --arms cm,zcode --tasks t6-polyfix --reps 2

# варианты: одно плечо --arms zcode; flash --model glm-5.3-flash;
# не пересекаться с номерами прошлых повторов: --rep-offset 19
# отчёт после: ... --report   (перегруппирует весь results.csv)
```

**Как ждать завершения** (важно, правило юзера): НЕ поллинговать каждые 30 с —
каждый вызов переплачивает чтение контекста. Запустить в фоне и ОДНИМ блокирующим
вызовом ждать маркера в логе:

```bash
.../python.exe scripts/bench_glm.py --run --arms cm,zcode --tasks t6-polyfix --reps 2 > .tmp/t6run.log 2>&1 &
timeout 590 bash -c 'until [ "$(grep -c "verify=" .tmp/t6run.log)" -ge 4 ]; do sleep 20; done'; tail -4 .tmp/t6run.log
```

Один прогон t6 — ожидаемо 2–6 мин (14 правок × Read+Edit + тесты + FIXES.md).

## 5. Что логировать и как читать

Харнесс сам пишет три потока данных:

1. **`runs/bench.log`** (и stdout) — строка на старт и на финиш каждого прогона:
   `=== <arm> <task> rep<N>: <status> verify=PASS wall=176.0s turns=18 in=5210 out=3500 cacheR=412000`
   Это главный контрольный сигнал; мониторить именно его.
2. **`runs/results.csv`** — по строке на прогон: `ts, arm, task, rep, status,
   verify_pass, wall_s, turns, tool_calls, tool_failures, in_tokens, out_tokens,
   cache_read, cache_write, reasoning_tokens, cost_usd, model, effort, notes`.
   Смысл: `in+cache_read` = полный контекст прогона; кэш-доля = cache_read/(in+cache_read).
3. **`runs/<arm>-<task>-r<rep>.json`** — транскрипты: у cm — entries лога менеджера
   (поля `Level`, `Message`, `ToolName` — `Level=="tool"` это вызов инструмента),
   у zcode — `events_digest` (вызовы инструментов в поле `tool`), `final_usage`,
   `meta`. Нужны для качественного сравнения «как течёт»: гистограмма инструментов,
   порядок ходов.

Критерий хорошего прогона: `status=completed`, `verify_pass=PASS`, `in_tokens>0`,
и для t6 — `tool_calls` ожидаемо **30+** (в этом смысл задачи: на t1–t5 медиана
была 5–6). Если `tool_calls` ≈ 14 — агент писал FIXES.md/файлы батчем, это тоже
валидно, отметить в выводах.

## 6. Механика плеч (для отладки, если что-то упадёт)

**Плечо cm** — последовательность RPC харнесса: `StartSessionWithModel{Project:
"GLM Lab", Session:"bench", Model, Effort}` (провайдер «glm» резолвится из model id
автоматически) → ждёт `session:status` match `{"id","status":"working"}` →
`SendMessage` → цикл `POST /wait {"attention":["GLM Lab/bench"],...}` до
`session:result` (в его `data.result.usage` — НАСТОЯЩИЕ токены; usage ассистентских
сообщений фасад зануляет) → `StopSession` (асинхронный — дождаться статуса ≠ working) →
`GetHistory`/`GetSessionLog` → `ClearSessionState`. Все запросы с заголовками
`X-CM-Token` (из `~/.claude-manager/control.json`) и `X-CM-Level: admin`.
ВНИМАНИЕ: значения `match` в /wait — сырые строки (`{"id": "GLM Lab/bench"}`),
НЕ JSON-кодированные — двойное кодирование молча не совпадает.

**Плечо zcode** — харнесс сам спавнит `%LOCALAPPDATA%\Programs\ZCode\ZCode.exe
resources\glm\zcode.cjs app-server --stdio` (env `ELECTRON_RUN_AS_NODE=1` +
`ZCODE_BUILTIN_PROVIDER_CONFIG_FILE`/`ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`),
пушит реестр провайдеров (`provider/updateAccountConfig`), `session/create`
(mode=`yolo`), `session/send` с modelSelection `{providerId:
"account:zai-individual-coding-plan", modelId:"GLM-5.3", options:{reasoningLevel:
"high"}}`, собирает события до `turn.completed`/`turn.failed`, убивает ребёнка.
Обратные запросы сервера харнесс уже отвечает (в т.ч. apiKey через
`interaction/requestProviderRuntimeHeaders`).

## 7. Известные грабли

- **Не запускать два батча параллельно** — песочница одна. Перед повторным
  запуском убедиться, что прошлый python-процесс мёртв.
- **Осиротевшие дети app-server**: если батч убит, ZCode.exe с `app-server --stdio`
  в командной строке может остаться. Убивать ТОЛЬКО их (отличать по командной
  строке через `Get-CimInstance Win32_Process`); десктопное приложение ZCode —
  БЕЗ аргументов, его не трогать.
- **Конфиг сессии до старта менеджера**: `sandbox/.claude-manager/config.toml`
  должен существовать ДО запуска менеджера (он кэширует проекты на старте);
  харнесс теперь пишет его первым делом, но при ручных экспериментах помнить.
- **flash**: на zcode-плече работает только под индивидуальным провайдером и
  требует явный `reasoningLevel`; пул start-plan отклоняет ключ Coding Plan
  («Provider authentication failed») — это находка, не баг харнесса.
- **Квота**: у t6 прогон тяжёлый (~0.3–0.6M контекст-токенов). При повторных
  API-ошибках/`turn_failed` — стоп и разбор, не жать дальше вслепую.
- Токены плеч приходят по разным проводам (result-событие vs turn.completed), но
  считаются от одного фасада — сравнимо; `cost_usd` есть только у cm.
- Правила юзера: коммиты только по явной просьбе; чужие проекты не трогать;
  `.tmp/` — черновик, в репо не попадает; вывод команд урезать (`tail`, `grep -c`).

## 8. Что сдать

1. Прогнать матрицу (п. 4), убедиться: все прогоны `completed` + `PASS`,
   токены ненулевые, `tool_calls` зафиксированы.
2. `--report` → `benchmarks/glmlab/report-<дата>.md`; добавить секцию по t6:
   медианы wall/turns/tool_calls/контекста по плечам, кэш-доля, сравнение с
   профилем t1–t5 (мелкошаговость zcode и его низкая кэш-доля — главный ожидаемый
   эффект), гистограммы инструментов из транскриптов.
3. Сверка плеча cm с самим менеджером: `GetHistory` — строка рана должна иметь
   ненулевые InputTokens (фикс c2f6201); расхождение с CSV = тревога.
4. Итог юзеру: таблица + 3–5 выводов с оговорками (n=2 на ячейку; микрозадачи;
   токены по разным проводам).
