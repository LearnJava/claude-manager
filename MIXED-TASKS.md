# Смешанное программирование — план работ (MP-01…MP-08)

Перенос в claude-manager рабочего процесса «смешанное программирование», отработанного
в lumen-browser (память проекта lumen: `feedback_mixed_programming_protocol`,
`reference_kilo_free_models_bench`): Claude готовит самодостаточные ТЗ, внешние
бесплатные модели пишут код FIND/REPLACE-патчами, манагер применяет их в изолированных
worktree, прогоняет ворота (build/lint/test), возвращает дефекты на доработку
(максимум 2–3 раунда) и выдаёт сравнительный отчёт о качестве моделей.

Каждая задача рассчитана на одну Claude Code сессию (~15-30 turns, ~5-10 файлов).
Правило проекта: каждая функция сопровождается тестом.

## Как сессия берёт задачу

Схема трекинга — канонная схема lumen (утв. 2026-06-23, память lumen
`feedback_status_pn_pointer_index`): очередь = голые указатели, мастер-список =
этот файл, статусы = таблица Status выше.

**Очередь: [MIXED-STATUS.md](./MIXED-STATUS.md)** — только строки-указатели
`MIXED-TASKS.md:NN` на заголовки открытых задач, приоритет сверху вниз, ничего
больше. `hasTasks()` манагера понимает этот формат (голая строка `<файл>:NN` =
открытая задача), поэтому очередь указывается как `task_source` — цикл сам
остановится, когда файл опустеет.

Правила для сессии:

1. Если в таблице Status выше есть задача `● IN PROGRESS` — продолжай ЕЁ
   (она была прервана).
2. Иначе возьми ВЕРХНИЙ указатель из `MIXED-STATUS.md`, все зависимости
   которого уже `✓ DONE` (зависимости — в шапке задачи и в графе ниже).
   Отметь её в таблице Status: `○ TODO` → `● IN PROGRESS`.
3. Выполни задачу по её спецификации в этом файле. Каждая функция — с тестом.
4. Завершение (только при зелёных `go build ./...` и `go test ./...`):
   - удали строку-указатель задачи из `MIXED-STATUS.md`;
   - в таблице Status: `● IN PROGRESS` → `✓ DONE`;
   - закоммить.
5. Одна задача = одна сессия. Не бери следующую.

**Переиндексация (обязательно):** вставил K строк в этот файл выше строки L —
сдвинь на +K все указатели `MIXED-TASKS.md:NN` с `NN ≥ L` в `MIXED-STATUS.md`.
Проверка указателя: `sed -n 'NNp' MIXED-TASKS.md` должен показать заголовок
`## MP-…`. Дописывай новые задачи в конец файла — сдвига не будет.

**Стартовый промпт сессии** (для ручного запуска или в конфиг манагера):

```
Прочитай CLAUDE.md и MIXED-TASKS.md. Следуй разделу «Как сессия берёт задачу»:
продолжи ● IN PROGRESS-задачу или возьми верхний доступный указатель из
MIXED-STATUS.md. Одна задача = одна сессия.
```

**Конфиг сессии в claude-manager:**

```toml
[[projects.sessions]]
name = "MP"
prompt = "<стартовый промпт выше>"
task_source = "MIXED-STATUS.md"
stop_when_no_tasks = true
```

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| MP-01 | ✓ DONE (2026-07-02) | internal/config/types.go, config.go, config_test.go, config.example.toml |
| MP-02 | ✓ DONE (2026-07-03) | internal/worker/client.go, persist.go, client_test.go, persist_test.go |
| MP-03 | ✓ DONE (2026-07-03) | internal/worker/patch.go, patch_test.go |
| MP-04 | ○ TODO | internal/worker/gates.go |
| MP-05 | ○ TODO | internal/worker/round.go, manager wiring |
| MP-06 | ○ TODO | internal/analysis/brief.go, schema |
| MP-07 | ○ TODO | cmd/fakeworker/, testdata/worker-scenarios/, e2e |
| MP-08 | ○ TODO | frontend/src/components/MixedRun.svelte, Settings |

## Рабочие модели (бенч lumen 2026-07-02, `.tmp/kilo_bench_results.json`)

Обе — через **Kilo Gateway** (`https://api.kilo.ai/api/gateway`, OpenAI-совместимый,
ключ `KILO_API_KEY`, JWT с app.kilo.ai → API Keys).

| Модель | model id | Роль | Качество |
|---|---|---|---|
| Step 3.7 Flash | `stepfun/step-3.7-flash:free` | «Руки» — генерация по жёсткому ТЗ | ~400 ток/с; модуль 420 строк с одного патча; фикс раунда за 9 с; НО: игнорирует фидбэк по своим тестам, изредка портит verbatim-FIND (выкидывает строку из анкора) |
| Nemotron 3 Ultra | `nvidia/nemotron-3-ultra-550b-a55b:free` | «Качество» — сложная логика, честный фидбэк-цикл | 5/5 тестов после раунда 2; качественный код с первого раунда; НО: изредка ломает формат патча (пропущен `>>>END`), кириллица в FIND-анкорах опасна — только ASCII |

Правила из боевого опыта lumen (зашиваются в код, а не в промпты по памяти):
- ТЗ (бриф) — самодостаточный: verbatim-выдержки кода, принятые архитектурные
  решения, typed-locals подсказки, формат патча. Для Step37 промпт **по-английски**.
- Тестам модели не верить (6/7 моделей пишут самопротиворечивые тесты) — ground
  truth и ворота всегда локальные.
- Троттлинг free-tier общий на ключ → запросы через один ключ строго
  последовательно (семафор per key_env).
- 403→429 — это троттлинг, не отзыв ключа: прогрессивный бэкофф, не фиксированные паузы.
- `finish_reason=length` → докачка «continue exactly where you stopped», потолок
  2–3 докачки (защита от зацикливания).
- Максимум 2–3 фидбэк-раунда, дальше — пометка `needs-human` (дочинивает Claude/человек).
- Фидбэк модели = точный вывод ворот (эмпирика), не пересказ.
- Free-эндпоинты логируют запросы → конфиденциальный код не слать; включение
  только явным флагом на проекте (privacy opt-in, как триггеры Laguna в lumen).

---

## MP-01: Конфиг воркеров

**Depends on:** ничего
**Files:** `internal/config/types.go`, `internal/config/config.go`, `config.example.toml`

Секция `[[workers]]` в TOML: `name`, `base_url`, `model`, `key_env`, `role`
(hands|quality|eyes), quirks: `reasoning_effort`, `max_output_tokens`,
`continuation_cap`, `ascii_anchors_only`, `request_timeout_sec`. Per-project:
`mixed_programming = true` (privacy opt-in, default false), `[project.gates]` —
список команд ворот. Валидация + дефолты (значения из таблицы моделей выше как
пресеты `step37`/`nemotron-ultra`). Тесты загрузки/валидации.

## MP-02: OpenAI-совместимый клиент воркера

**Depends on:** MP-01
**Files:** `internal/worker/client.go`, `internal/worker/persist.go`

Chat completions (streaming) поверх `net/http`, без SDK. Обязательное поведение
(каждый пункт — из готч lumen, каждый — с тестом на httptest-стабе):
- `reasoning: {effort: low}` по умолчанию (иначе step37 сжигает токены на thinking);
- прогрессивный бэкофф на 403/429 (растущий sleep, jitter), потолок сбоев подряд →
  терминальная ошибка раунда;
- докачка по `finish_reason=length` (append assistant-кусок + «continue exactly
  where you stopped»), потолок `continuation_cap`;
- пересоздание http.Client при transport-ошибках (отравленный keepalive → SSL-сбои);
- глобальный семафор per `key_env` — один запрос на ключ одновременно;
- UTF-8 всегда через `json.Marshal` тела (не шелл-интерполяция);
- персист диалога (`messages`) в `~/.claude-manager/state/worker-<id>.json` после
  каждого обмена — API stateless, resume только переигрыванием.

## MP-03: Формат патчей + аппликатор

**Depends on:** ничего (параллельно MP-02)
**Files:** `internal/worker/patch.go`, `patch_test.go`

Порт `lumen-browser/.tmp/apply_patches.py` на Go. Формат:
`### PATCH n` / `FILE <path>` / `<<<FIND` / `===REPLACE` / `>>>END`.
- Парсер устойчив к типовым поломкам моделей: пропущенный `>>>END` между патчами
  (сплайс с диагностикой — кейс nemotron-ultra), `===END` вместо `>>>END`;
- валидация до применения: FIND встречается в файле ровно один раз, verbatim
  (кейс step37 — выкинутая строка из анкора → патч отклоняется с точным diff
  ожидания/реальности в фидбэк);
- опция `ascii_anchors_only` — предупреждение при генерации брифа;
- применение только внутри worktree (аналог `_safe_path`), запись атомарная.
Golden-тесты на образцах реальных поломок из lumen-раундов.

## MP-04: Ворота (gates)

**Depends on:** MP-01
**Files:** `internal/worker/gates.go`, `gates_test.go`

Прогон команд из `[project.gates]` в worktree последовательно, стоп на первом
ненулевом коде. Захват stdout+stderr с обрезкой (лимит на фидбэк ~14КБ, как в
lumen). Результат: `ok` → коммит в worktree (`Co-Authored-By: <model>`);
`fail` → склеенный вывод как фидбэк-сообщение. Отличие от `post_task_hook`:
ворота **блокирующие** — красные ворота отклоняют результат. Тесты на фейковых
командах (exit 0/1, длинный вывод).

## MP-05: Оркестратор раундов

**Depends on:** MP-02, MP-03, MP-04
**Files:** `internal/worker/round.go`, `internal/session/manager.go` (wiring), `app.go`

Цикл на подзадачу: создать worktree+ветку (`mp-<task>-<model>-<HHMMSS>`) → бриф
воркеру → распарсить патчи → применить → ворота → зелёные: коммит, статус `done`,
worktree остаётся на ревью; красные: фидбэк (вывод ворот + отклонённые патчи) →
следующий раунд; после `max_rounds` (default 3) — статус `needs-human`.
- Все события через `Emitter` (`worker:round`, `worker:patch`, `worker:gate`,
  `worker:done`) — control-plane и MCP получают их бесплатно;
- крашеустойчивость: состояние раунда + messages персистятся (MP-02), resume
  с места обрыва;
- Wails-биндинги: `DispatchMixedTask(project, briefID, workerName)`,
  `GetMixedRounds(project)`, `CancelMixedTask(id)`.
Тесты: полный цикл против стаба-воркера (зелёный с 1 раунда; красный→фидбэк→зелёный;
исчерпание раундов).

## MP-06: Генерация брифов Claude-сессией

**Depends on:** MP-05 (типы), существующий internal/analysis
**Files:** `internal/analysis/brief.go`, `internal/analysis/schema.go` (расширение)

Preflight-механика (haiku/sonnet, `--json-schema`) генерирует бриф: описание
задачи **на английском**, verbatim-выдержки затронутого кода с точными строками,
принятые решения (никаких «выбери между A и B»), typed-locals подсказки,
инструкция формата патча, список файлов. Приём из lumen: для нового файла —
предсоздать файл с `// PLACEHOLDER`-якорем и просить патч через FIND по нему.
JSON Schema + валидация выхода. Брифы в SQLite (`mixed_briefs`). Тесты схемы
и сборки промпта.

## MP-07: fakeworker + e2e

**Depends on:** MP-02..MP-05
**Files:** `cmd/fakeworker/`, `testdata/worker-scenarios/*.json`, `internal/control/e2e_*`

По образцу fakeclaude: HTTP-сервер, отдающий скриптованные OpenAI-совместимые
ответы по сценарию. Обязательные сценарии: чистый патч с 1 раунда;
битый патч → фидбэк → чистый; 429-шторм (проверка бэкоффа);
`finish_reason=length` ×2 (докачка); зацикленная докачка (проверка потолка);
пропущенный `>>>END`. E2e-сценарии в `testdata/e2e/` через control-plane.
MCP-инструменты: `dispatch_mixed_task`, `get_mixed_rounds`, `wait_for_worker_status`.

## MP-08: UI

**Depends on:** MP-05, MP-07
**Files:** `frontend/src/components/MixedRun.svelte`, `Settings.svelte` (вкладка Workers),
`frontend/src/stores/workers.ts`

- Таймлайн раундов подзадачи: модель, № раунда, применённые/отклонённые патчи,
  вывод ворот (сворачиваемый), вердикт, ссылка на worktree/ветку;
- сравнительный отчёт качества: модель × (раундов до зелёного, чистых патчей %,
  дефекты по типам) — данные из SQLite, отображение в CostDashboard или отдельно;
- Settings: CRUD воркеров, privacy-флаг проекта с предупреждением
  «код уходит на внешние серверы»;
- Playwright-спеки против fakeworker.

---

## Граф зависимостей

```
MP-01 (конфиг) ──┬── MP-02 (клиент) ──┐
                 └── MP-04 (ворота) ──┼── MP-05 (оркестратор) ── MP-06 (брифы)
MP-03 (патчи, независим) ─────────────┘         │
                                                ├── MP-07 (fakeworker + e2e)
                                                └── MP-08 (UI)
```

Параллельно можно: MP-02 + MP-03 + MP-04 (после MP-01).

## Вне скоупа (осознанно)

- Laguna M.1, laguna-xs.2, north-mini-code — по бенчу непригодны для кода
  (медленно/нестабильно/удаляют тесты).
- nemotron-3-nano-omni — только как «глаза» (описание скриншотов), не для
  FIND/REPLACE-патчей; отдельной задачей после MP-08, если понадобится.
- Автоматический merge в main — итог всегда ревьюит Claude-сессия или человек
  (worktree и ветка остаются).
