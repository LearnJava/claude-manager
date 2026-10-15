# CTX-TASKS — заполненность контекста считается по последнему вызову модели

Полоса «контекст» в карточке сессии (`SessionCard.svelte`) и порог
`context_handoff` (`Session.checkContextRestart`) должны показывать, сколько
токенов уходит в модель **в одном вызове** — это и есть занятость окна. Сейчас
оба места берут сумму по многим вызовам.

## Почему (2026-10-15, Chat)

Сессия ARB/Chat показывала «контекст 100%» и 698K токенов при окне 200K.

1. **`SessionManager.handleResult`** (`internal/session/manager.go`, ~стр. 1868)
   в конце хода пересчитывает `ms.contextUtil` из накопленных счётчиков:
   ```go
   used := ms.inputTokens + ms.cacheReadTokens + ms.cacheCreateTokens
   ms.contextUtil = float64(used) / float64(ms.contextWindow)
   ```
   `handleUsage` прибавляет к этим счётчикам usage **каждого** сообщения
   ассистента, а каждый вызов перечитывает весь контекст из кэша
   (`cache_read`). 20 вызовов по 35K = 700K → 350% → карточка обрезает до 100%
   (`SessionCard.svelte:85`). `handleUsage` до этого считал правильно — по
   одному вызову — но `handleResult` это затирает, и снапшоты
   (`ContextUtil: ms.contextUtil`, стр. ~1339 и ~1419) отдают фронтенду
   неверное значение.
2. **`Session.checkContextRestart`** (`internal/session/session.go`, ~стр. 2008)
   кормит `ContextMonitor.Observe` полем `res.Usage` события `result`. В
   stream-json Claude CLI `result.usage` — сумма по всем вызовам хода, а не
   последний вызов, поэтому `context_handoff` может срабатывать раньше порога.
   Проверить на реальной записи (`testdata/` или лог из
   `.claude-manager/logs/`): сравнить `result.usage` с usage последнего
   `assistant`. `checkHandoffBudget` уже работает правильно — по usage каждого
   сообщения (`session.go`, ветка `EventLog`, ~стр. 1867).

## Инварианты блока

- Накопленные счётчики `ms.inputTokens/outputTokens/cacheReadTokens/cacheCreateTokens`
  остаются как есть — по ним считаются расход, стоимость, история прогонов.
  Меняется только то, что называется «заполненность контекста».
- Поведение `context_handoff` при выключенном флаге не меняется.

## Как сессия берёт задачу

Та же схема, что у `QUEUE-TASKS.md`: указатель `CTX-TASKS.md:NN` в
[STATUS-P1.md](./STATUS-P1.md), статус — таблица в конце файла, протокол —
`/cm-task-start` и `/cm-task-finish` по
[docs/git-workflow.md](./docs/git-workflow.md). Гейт:
`go build ./... && go vet ./... && go test ./...` (+ `npm --prefix frontend test`,
если трогали фронтенд). Новые задачи дописывать в конец файла, чтобы не
сдвигать указатели.

---

## CTX-01: заполненность контекста = размер последнего вызова, а не сумма

**Depends on:** —

**Files:**
- `internal/session/manager.go` — `managedSession`, `handleUsage`, `handleResult`, сброс счётчиков (~стр. 1786)
- `internal/session/session.go` — `checkContextRestart`, ветка `EventLog`
- `internal/session/manager_test.go`, `internal/session/session_contexthandoff_test.go`
- `docs/design-decisions.md` — раздел про context handoff / токен-метрики (одна-две фразы)

**What to do:**
1. В `managedSession` добавить `lastContextTokens int` — `input + cache_read +
   cache_creation` последнего usage-события. `handleUsage` записывает его и
   считает `contextUtil` от него (как сейчас). Сбрасывать вместе с остальными
   счётчиками при старте прогона.
2. `handleResult`: по-прежнему подхватывает `contextWindow` из `ModelUsage`, но
   `contextUtil` пересчитывает от `ms.lastContextTokens`, не от
   накопленной суммы. Если окно стало известно только сейчас — тоже от
   последнего вызова. После этого вызвать `emit(EventNameContext, …)` с
   правильной `Utilization`, чтобы карточка обновилась сразу, а не только
   из снапшота.
3. `Session`: запоминать usage последнего сообщения ассистента в ходе (поле,
   обновляется в ветке `EventLog` рядом с `checkHandoffBudget`).
   `checkContextRestart` строит `TokenUsageAdapter` из него, а не из
   `res.Usage`; если за ход ни одного usage не было — fallback на `res.Usage`.
   Сначала убедиться на реальной записи stream-json, что `result.usage`
   действительно суммарный; если окажется, что нет — шаг 3 не делать и
   записать это в «Почему» этого файла.
4. Тесты:
   - `manager_test`: несколько usage-событий (например 30K, 32K, 35K
     при окне 200K) и затем result с `contextWindow = 200000` →
     `ContextUtil ≈ 0.175`, а не сумма (`0.485`); накопленные
     `InputTokens/CacheRead…` в метриках — по-прежнему суммы.
   - `session_contexthandoff_test`: ход с usage по 40K в каждом сообщении,
     `result.usage` = сумма > порога, порог 0.5 при окне 200K → рестарт
     **не** срабатывает; при последнем usage выше порога — срабатывает.
5. В `docs/design-decisions.md` (раздел о context handoff / метриках токенов)
   одной фразой: заполненность контекста — по usage последнего вызова, суммы —
   только для расхода.

**Done when:**
- `go build ./... && go vet ./... && go test ./...` зелёные, новые тесты из п. 4
  проходят и падают на старом коде.
- В работающем менеджере у сессии с длинным ходом полоса «контекст» после
  завершения хода показывает примерно размер последнего вызова (десятки
  процентов), а не 100%.
- Строка CTX-01 в таблице Status — `✓ DONE`, указатель удалён из `STATUS-P1.md`.

---

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| CTX-01 | ○ TODO | internal/session/manager.go, session.go |
