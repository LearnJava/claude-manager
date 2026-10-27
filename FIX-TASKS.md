# FIX-TASKS — баги, найденные при разборе ленты сессии Lumen/S4 (2026-10-27)

Очередь — [STATUS-P1.md](./STATUS-P1.md). Задачи независимы друг от друга.

Как запускать GUI-тесты Playwright из сессии внутри менеджера (пока не сделана
FIX-02): `cd frontend && env -u CM_CONTROL_TOKEN -u CM_CONTROL_PORT npx playwright test <spec>`.
Если на машине уже запущен чужой Vite на 5173, добавьте `CM_VITE_PORT=5199`.

## FIX-01: Лог работающей сессии через cm-mcp пустой

**Depends on:** —

**Files:** `internal/session/manager.go`, `internal/control/rpc_logs.go`, `internal/session/manager_test.go` (или новый `*_test.go` рядом), `docs/mcp.md` (только если меняется описание инструментов — через `go run ./cmd/cm-mcp -gen-docs`)

**Что не так.** `get_session_logs` и `search_session_log` у работающей сессии
возвращают `entries: []`, хотя GUI показывает ленту. Воспроизведение: сессия
`Lumen browser/S4` в статусе `working`, 53 вызова инструментов в транскрипте —
MCP отдаёт пустой список. Причина: `SessionManager.GetSessionLog`
(`internal/session/manager.go`, ~стр. 1429) читает только SQLite
(`store.ListRuns(..., 1)` → `store.GetLogs`), а записи текущего прогона копятся в
памяти в `ms.pendingLogs` и пишутся в базу только в конце прогона
(`store.InsertLogs` в функции завершения прогона, ~стр. 2084). Для идущего прогона
строка в `session_runs` уже есть, а логов в ней нет. `SearchSessionLog`
(`internal/control/rpc_logs.go`) берёт данные из того же места.

**Что сделать.**
1. В `GetSessionLog`: если последний прогон из `ListRuns` — это текущий
   незавершённый прогон сессии (его id совпадает с id активного прогона в
   `managedSession`), отдавать записи из `ms.pendingLogs` (копию под `ms.mu`),
   с тем же `offset`/`limit` и в том же типе `[]*store.LogEntry`. Для
   завершённых прогонов поведение не меняется.
2. Проверить, что `SearchSessionLog` после этого видит живые записи (если он
   читает не через `GetSessionLog` — перевести на него же).
3. Тест на уровне `SessionManager` с fakeclaude/testkit: сессия в середине
   прогона (сценарий, который ждёт ввода, см. `testdata/scenarios/scenarios_doc.md`),
   `GetSessionLog` возвращает уже пришедшие записи; после завершения прогона —
   те же записи из SQLite, без дублей.

**Done when:** `go build ./... && go vet ./... && go test ./internal/session/ ./internal/control/`
зелёные; новый тест падает на старом коде и проходит на новом; у работающей
сессии `get_session_logs` возвращает записи текущего прогона.

## FIX-02: Playwright-тесты из сессии менеджера подключаются к самому менеджеру

**Depends on:** —

**Files:** `frontend/tests/global-setup.ts`, `frontend/tests/fixtures.ts`, `frontend/tests/helpers/control.ts`, `docs/testing-harness.md`

**Что не так.** `global-setup.ts` (стр. ~42) считает любой заданный
`CM_CONTROL_TOKEN` признаком «внешний сервер уже запущен» и не поднимает свой
`playwright-server`. Но менеджер выставляет `CM_CONTROL_TOKEN` (и
`CM_SESSION_ID`) в окружение **каждой** сессии, которую запускает, — для
`cm-mcp`. Поэтому `npx playwright test` из любой сессии менеджера пытается
подключиться к `127.0.0.1:7334` с токеном настоящего менеджера и падает с
`ECONNREFUSED` (или, хуже, ходит в настоящий менеджер).

**Что сделать.**
1. Ввести отдельную переменную для внешнего сервера тестов, например
   `CM_PW_CONTROL_TOKEN` (+ `CM_PW_CONTROL_PORT`). Только она включает режим
   «внешний сервер». `CM_CONTROL_TOKEN`/`CM_CONTROL_PORT` из окружения
   `global-setup` в начале игнорирует и перезаписывает своими значениями
   (как уже делает на стр. ~108), чтобы `fixtures.ts` и `helpers/control.ts`
   получили токен тестового сервера.
2. Обновить комментарий в шапке `global-setup.ts` и раздел про Playwright в
   `docs/testing-harness.md`: как подключиться к уже запущенному серверу.

**Done when:** из сессии менеджера (где `CM_CONTROL_TOKEN` задан) команда
`cd frontend && npx playwright test tests/log-feed.spec.ts` без `env -u` поднимает
свой сервер и проходит; с `CM_PW_CONTROL_TOKEN` используется внешний сервер.

## FIX-03: В тестовом мосте нет GetSessionRoadmap и других методов

**Depends on:** —

**Files:** `frontend/tests/helpers/bridge.ts`, новый `frontend/tests/bridge-coverage.spec.ts`

**Что не так.** В GUI-тестах панель «Дорожная карта» показывает красным
`window.go.main.App.GetSessionRoadmap is not a function`: мост
(`frontend/tests/helpers/bridge.ts`, блок `window.go.main.App stubs`) — ручной
список заглушек, и `GetSessionRoadmap` (вызывается из
`frontend/src/stores/queues.ts`) в нём нет. В control-plane RPC он есть
(`internal/control/rpc_planning.go`, параметры `project`, `session`).

**Что сделать.**
1. Добавить заглушку `GetSessionRoadmap: (project, session) => rpc('GetSessionRoadmap', { project, session })`.
2. Найти остальные пропуски: собрать имена всех методов, которые фронтенд
   импортирует из `wailsjs/go/main/App` (`grep -rho "from '.*wailsjs/go/main/App'"`
   по `frontend/src` + имена в фигурных скобках импорта), и сравнить со списком
   ключей в мосте. Для каждого пропущенного метода, у которого есть RPC в
   `internal/control/rpc*.go`, добавить заглушку с правильными именами параметров;
   для методов без RPC — заглушку, возвращающую `null`/пустое значение, с
   комментарием почему.
3. `bridge-coverage.spec.ts`: тест без браузера, который читает исходники
   `frontend/src` и `bridge.ts` и падает, если фронтенд импортирует метод `App`,
   которого нет в мосте, — с именем метода в сообщении.

**Done when:** `bridge-coverage.spec.ts` проходит (и падает, если удалить заглушку
`GetSessionRoadmap`); в тесте `log-feed.spec.ts` панель «Дорожная карта» не
показывает ошибку `is not a function`.
