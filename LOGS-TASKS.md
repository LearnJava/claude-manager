# LOGS-TASKS — логи всех компов в одном репозитории GitHub (2026-10-27)

Очередь — [STATUS-P1.md](./STATUS-P1.md). Порядок: LG-01 → LG-02 → LG-03 → LG-04.

Менеджер работает на 3–4 компах, и у каждого свои логи: логи прогонов
`<проект>/.claude-manager/logs/*.md` (в `.gitignore`, через git проекта не
ездят) и логи самого менеджера `~/.claude-manager/logs/*.log` (`app.log`,
`crash.log`, `cmdguard.log`). Чтобы разбирать работу менеджера по всем компам
сразу, каждый менеджер отправляет свои логи в приватный репозиторий
`git@github.com:LearnJava/cm-logs.git`, в папку своего компа:

```
cm-logs/
├── ANALYZED.md                          пишет только скилл разбора; менеджер его не трогает
└── logs/<host>/
    ├── machine.md                       сборка менеджера, проекты, время последней отправки
    ├── manager/<имя>-<ГГГГ-ММ-ДД>.log   новые байты app.log / crash.log / cmdguard.log за день
    └── <проект>/<ГГГГ-ММ>/<файл лога>.md
```

Разбирает логи Claude по просьбе пользователя — скилл `/cm-logs-review`
(раздел «Разбор» в конце); в эту очередь он не входит.

## Инварианты блока

- **Выключено по умолчанию.** Пустой `logs_repo` — ничего не клонируется и не
  отправляется.
- **Проекты только читаются.** В папки проектов ничего не пишется.
- **Только своя папка.** Менеджер меняет в репозитории только `logs/<host>/`;
  чужие папки и `ANALYZED.md` не трогает. Поэтому push с разных компов не
  конфликтуют.
- **Без force-push, переписывания истории и `git config`.** Идентичность и
  `core.autocrlf=false` передаются флагами `-c` на каждую команду, как `git` в
  `internal/mirror/mirror.go` (~стр. 451).
- **Секреты маскируются до записи в клон** (LG-03). До LG-04 пакет никто не
  вызывает, так что без маскировки ничего не уходит.
- **Репозиторий могут пересоздать руками** (чистка раз в полгода). Менеджер
  подхватывает новый репозиторий сам и не отправляет повторно то, что уже
  отправил.

## LG-01: Имя компа и сборка менеджера в логах

**Depends on:** —

**Files:** новый `internal/buildinfo/buildinfo.go` (+ `buildinfo_test.go`), `internal/fsutil/` (общее правило имени папки), `internal/mirror/mirror.go`, `internal/store/logfiles.go`, `internal/experience/mdlog.go` (+ тест), `app.go`, `docs/design-decisions.md`, `docs/architecture.md`

**Зачем.** Разбор касается самого менеджера: по логу нужно видеть, с какого
компа он и какой сборкой написан, иначе разбор предложит чинить уже
починенное. Сейчас заголовок md-лога — только `# Session log — <id>` и
`_Saved <время>, N entries._`.

**Что сделать.**
1. Правило имени папки из `mirror.DirName` (`internal/mirror/mirror.go`,
   ~стр. 123) вынести в `internal/fsutil` (например `fsutil.SafeName`);
   `mirror.DirName` вызывает его, поведение зеркала не меняется.
2. Пакет `internal/buildinfo`:
   - `Revision() string` — `vcs.revision` из `debug.ReadBuildInfo()`, первые 7
     символов, плюс `-dirty` при `vcs.modified=true`; `unknown`, если данных VCS
     нет (`go test`, сборка вне git).
   - `Host() string` — `os.Hostname()` через `fsutil.SafeName`; `unknown-host`
     при ошибке. Имя берётся из Windows, а не из конфига: `config.toml`,
     скопированный на другой комп, не должен склеить два компа в одну папку.
3. `store.RenderExport` (`internal/store/logfiles.go`, ~стр. 35):
   - md — после строки `_Saved …` отдельная строка `_Machine <host> · build <rev>_`
     и пустая строка. Строку `_Saved …` не менять: `mdSavedRe`
     (`internal/experience/mdlog.go`, ~стр. 143) матчит её целиком;
   - txt — строка `Machine <host> · build <rev>` после `Saved …`;
   - json — поля `machine` и `build` рядом с `session`.
4. `experience.ParseLogFile`: регулярка для строки `_Machine …_`, значения — в
   новые поля `Trajectory.Machine` и `Trajectory.Build`; строка не идёт в
   `Skipped`. Старые логи без неё разбираются как раньше.
5. Стартовая строка `app.log` (`logger.L.Info("startup", …)` в `app.go`,
   ~стр. 72): добавить `host` и `build`.
6. Doc-sync: «Automatic Log Saving» в `docs/design-decisions.md` (новая строка
   заголовка и зачем она), дерево `docs/architecture.md` (`internal/buildinfo`).

**Done when:** `go build ./... && go vet ./... && go test ./internal/buildinfo/ ./internal/fsutil/ ./internal/mirror/ ./internal/store/ ./internal/experience/`
зелёные; тест `ParseLogFile` на файле с новой строкой — `Skipped == 0`,
`Machine`/`Build` заполнены; файл без неё разбирается как раньше.

## LG-02: `internal/logship` — отправка логов прогонов в репозиторий

**Depends on:** LG-01

**Files:** новые `internal/logship/logship.go`, `internal/logship/state.go`, `internal/logship/logship_test.go`; `docs/architecture.md`

**Что сделать.** Один проход `Ship(ctx, Options) (Result, error)`, без
таймеров и UI (это LG-04).
1. `Options`: `Repo` (URL; в тестах — путь к локальному bare-репо), `Host`,
   `Build` (из `internal/buildinfo`), `DataDir` (папка данных менеджера — там
   же, где `config.toml` и `history.db`, см. `defaultDBPath` в `app.go`),
   `Projects []Project` (`Name`, `Path`), `Now func() time.Time` (для тестов).
2. Клон — `<DataDir>/cm-logs`, ветка `main`. Проход:
   1. Нет клона → `git clone <Repo> <DataDir>/cm-logs`.
   2. `git fetch origin`. Есть `origin/main` → `git reset --hard origin/main`
      (своих правок в клоне не бывает; коммит неудачного прошлого прохода
      просто выбрасывается). Нет `origin/main` (репозиторий пуст: новый или
      пересоздан) → если в клоне есть коммиты, удалить его и склонировать
      заново; встать на `main` (`git symbolic-ref HEAD refs/heads/main`).
   3. Скопировать новые логи (п. 3).
   4. `git add -A logs/<Host>`; изменений нет → проход окончен без коммита.
   5. `git commit -m "<Host>: <N> logs, build <Build>"`, затем
      `git push origin HEAD:main`.
   6. Push не прошёл (другой комп успел запушить, сеть) → снова с шага 2,
      всего не больше 3 попыток. Rebase не нужен: шаг 2 всегда начинает от
      `origin/main`, а отметки ещё не сдвинуты, так что шаг 3 положит те же
      файлы.
   7. Только после успешного push сохранить новые отметки (п. 4).

   Git — свой хелпер по образцу `mirror.git`: флаги `-c`, `proc.HideConsole`,
   переменная окружения `GIT_TERMINAL_PROMPT=0`, таймаут 2 мин, для
   clone/fetch/push — 15 мин. Ошибка сети или ключа — текст в `Result.Error`,
   а не зависание.
3. Логи прогонов: у каждого проекта — `*.md` из `<Path>/.claude-manager/logs/`
   (`*.tmp` пропускать). Время сохранения — из имени
   (`…-20261027-222114.273201.md`, формат `store.SaveSessionLogFile`). Берутся
   файлы новее отметки проекта и старше 2 минут (соседний прогон мог ещё не
   дописать свой). Кладутся в
   `logs/<Host>/<fsutil.SafeName(имя проекта)>/<ГГГГ-ММ>/<имя файла>`. Папки
   проекта нет → проект пропускается, остальные идут.
4. Отметки — `<DataDir>/logship.json`, **вне клона**: по проекту — время
   последнего отправленного файла (строка из имени, сравнивается
   лексикографически). Нет отметки → логи за последние 7 дней. Вне клона —
   чтобы пересозданный руками репозиторий не получил заново всё старое.
5. `Result`: `At`, `Files`, `Bytes`, `Pushed bool`, `Error string`.

**Тесты** (вместо GitHub — `git init --bare` во временной папке):
- первый проход кладёт логи в `logs/<host>/<проект>/<ГГГГ-ММ>/`; повторный без
  новых файлов — без нового коммита;
- файл моложе 2 минут и `*.tmp` не берутся; следующий проход берёт файл,
  когда он «состарился»;
- между проходами в remote пушит второй клон («другой комп») — проход
  успешен, чужие файлы на месте и не тронуты;
- remote пересоздан пустым — уходят только файлы новее отметки;
- remote недоступен → `Result.Error` заполнен, отметки не сдвинуты, следующий
  удачный проход отправляет всё накопленное.

**Done when:** `go build ./... && go vet ./... && go test ./internal/logship/`
зелёные, тесты выше есть; пакет в дереве `docs/architecture.md`.

## LG-03: Логи менеджера, `machine.md` и маскировка секретов

**Depends on:** LG-02

**Files:** `internal/logship/logship.go`, `internal/logship/state.go`, новый `internal/logship/mask.go`, тесты рядом

**Что сделать.**
1. `Options.ManagerLogsDir` — папка, куда `internal/logger` пишет `app.log`.
   Каждый `*.log` оттуда (`app.log`, `crash.log`, `cmdguard.log`): байты от
   сохранённого смещения до конца дописываются в
   `logs/<Host>/manager/<имя без .log>-<ГГГГ-ММ-ДД>.log` (дата прохода; файл
   этого дня мог прийти из `origin/main` — дописывать в него). Файл стал
   короче смещения → смещение 0. Нет смещения → файл целиком. Смещения — в
   `logship.json` и сдвигаются только после успешного push, поэтому повторная
   попытка после reset не задваивает строки.
2. `logs/<Host>/machine.md` переписывается каждый проход: хост, сборка, версия
   Go (`runtime.Version()`), время прохода, проекты (имя → папка на этом компе →
   время последнего отправленного лога).
3. `mask.go`: `Mask(s string, extra []string) string` — `sk-ant-…`, `ghp_…`,
   `gho_…`, `github_pat_…`, `AKIA…`, токены Telegram-ботов
   (`\d{8,10}:[A-Za-z0-9_-]{35}`), блоки `-----BEGIN … PRIVATE KEY-----` и
   строки из `extra` → `***`. Через неё проходит всё, что пишется в клон: логи
   прогонов, логи менеджера, `machine.md`. `Options.ExtraSecrets` передаётся в
   `extra` (LG-04 кладёт туда токен control-plane).

**Done when:** `go test ./internal/logship/` зелёный, новые тесты:
дописывание со смещения и сброс при укороченном файле; повторная попытка после
отклонённого push не задваивает строки; `machine.md` есть; каждый тип секрета
из списка и строка из `extra` заменены на `***` и в логах прогонов, и в логах
менеджера.

## LG-04: Включение отправки — настройки, таймер, Settings, MCP

**Depends on:** LG-03

**Files:** `internal/config/types.go`, `internal/config/config.go`, новый `app_logship.go`, `app.go`, `frontend/src/components/Settings.svelte`, строки в `frontend/src/lib/locales/{en,ru}/`, `internal/control/mcptools_projects.go`, `internal/control/rpc_projects.go`, `internal/control/access.go`, `config.example.toml`, `PLAN.md`, `docs/architecture.md`, `docs/design-decisions.md`, `docs/mcp.md` (через `-gen-docs`)

**Что сделать.**
1. `GlobalSettings`: `LogsRepo string` (`logs_repo`, пусто = выключено) и
   `LogsIntervalMin int` (`logs_interval_min`, по умолчанию 60, 0 = только
   кнопка). Описать рядом с `mirror_dir` в `config.example.toml` и в PLAN.md.
2. `app_logship.go` по образцу `app_mirror.go`: первый проход через 30 с после
   старта, дальше каждые `logs_interval_min` и по пинку (кнопка, сохранение
   настроек — как `kickMirror`); проходы не пересекаются. `logship.Options` из
   конфига: все проекты `a.cfg.Projects`, `DataDir` — папка `config.toml`,
   `ManagerLogsDir` — папка `app.log`, `ExtraSecrets` — токен control-plane
   (`internal/control/endpoint.go`). Последний `Result` хранится для статуса;
   ошибка — `logger.L.Warn("logship", …)`.
3. Методы `App`: `ShipLogsNow() (logship.Result, error)` и
   `GetLogShipStatus() LogShipStatus` (`enabled`, `repo`, `host`, `lastRun`,
   `files`, `error`). Строки в «Wails Bindings» (`docs/architecture.md`).
4. MCP: `ship_logs_now` (operate, идемпотентный) и `get_log_ship_status`
   (чтение) — по образцу `sync_project_mirrors` (`mcptools_projects.go`,
   `rpc_projects.go`, `access.go`); затем `go run ./cmd/cm-mcp -gen-docs`.
5. Settings → Global, рядом с настройками зеркала: поле «Репозиторий логов»,
   интервал, статус (последняя отправка, сколько файлов, ошибка), кнопка
   «Отправить сейчас». Подсказка: «Логи прогонов всех проектов и логи
   менеджера уходят в logs/<имя компа>/ этого репозитория. Пусто — выключено.»
   RU и EN.
6. `docs/design-decisions.md`: раздел «Log Shipping (cm-logs)» — зачем,
   раскладка репозитория, отметки вне клона, reset к `origin/main` вместо
   rebase, пересоздание репозитория, маскировка.

**Done when:** `go build ./... && go vet ./... && go test ./...` и фронтовые
тесты (как в `/cm-task-finish`) зелёные, включая parity-тест
(`app_parity_test.go`) и `TestDocsInSync`; с `logs_repo` = путь к локальному
bare-репо «Отправить сейчас» кладёт логи в `logs/<host>/` и показывает статус;
с пустым `logs_repo` клон не создаётся.

## Разбор (не в этой очереди)

Скилл `/cm-logs-review` в `.claude/skills/` и скрипт сводки делаются в Chat
после первой недели логов, на реальных данных. Порядок разбора: свой клон
`cm-logs` (не клон менеджера) → `git pull` → новые файлы с прошлого разбора:
`git diff --name-only --diff-filter=A <коммит из ANALYZED.md>..HEAD` (по
коммиту, а не по дате: логи с выключенного компа приходят позже) → скрипт
сводит их по компам и сборкам → Claude читает логи за аномалиями → сверка с
историей claude-manager → рекомендации и задачи для Developer 1 → новый
разобранный коммит в `ANALYZED.md`.

Чистка репозитория (раз в полгода, руками): сначала разбор, потом удалить и
создать `cm-logs` заново. Менеджеры подхватят пустой репозиторий сами
(LG-02, п. 2) и старое не отправят (отметки вне клона).
