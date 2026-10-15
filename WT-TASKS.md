# Рабочая папка сессии: постоянный слот вместо `claude --worktree` — план работ (WT-01…WT-03)

Цель блока: галка **«Use worktree»** перестаёт передавать CLI голый `--worktree`
и означает «у сессии своя постоянная рабочая папка» — слот пула
`<project>/.claude/worktrees/<slot>`, тот же, что ведёт протокол
(`scripts/worktree-pool.sh`, `/cm-task-start`, `/cm-task-finish`).

## Почему (замер 2026-10-15, Lumen S1)

- С `use_worktree = true` и пустым `worktree_name` менеджер передаёт
  `--worktree` без имени (`internal/session/session.go` ~2485,
  `internal/session/hermes_runtime.go` ~434). CLI на **каждом** старте создаёт
  новый worktree со случайным именем и **до первого события `init` молчит**:
  на Lumen (62 000 файлов) это 63–334 с, в пустом репозитории — 2 с. С 13.10
  медиана spawn→init по `app.log` выросла с 2–4 с до 55–82 с.
- Сессия этот worktree не использует: по протоколу она сразу уходит в свой слот
  `p<N>-work`. CLI в режиме `-p` за собой не убирает, менеджер — тоже: в Lumen
  накопилось 42 брошенных checkout'а.
- Незаконченная задача в свежем worktree от HEAD не видна — ровно тот инцидент,
  от которого защищает протокол (см. комментарий в `app.go` ~577).

## Инварианты блока

1. **Ветками управляет протокол, не менеджер.** Менеджер только гарантирует,
   что папка слота существует и исправна, и запускает в ней CLI. Ветку слота он
   не переключает, не коммитит, не сбрасывает (кроме починки прерванного
   checkout'а — гард 3 из `worktree-pool.sh`).
2. **Ничего не пишется в отслеживаемые файлы пользователя без подтверждения в
   UI.** Создание слота трогает только `.claude/worktrees/` и локальный
   `.git/info/exclude`. Файлы протокола и раздел в `CLAUDE.md` — только по
   кнопке (WT-03).
3. **Совместимость с существующими пулами.** Слоты Lumen (`p1-work`, `p2-work`,
   `p3-work`, `p6-work`, `perf-base`) подхватываются как есть; собственный
   `scripts/worktree-pool.sh` проекта не перезаписывается.
4. `s.ProjectPath` остаётся корнем проекта для всего, что делает менеджер
   (очередь `task_source`, логи, хуки, primer, handoff). Меняется только
   рабочая папка процесса CLI.

---

## WT-01: пакет `internal/worktree` — слот сессии

**Depends on:** —

**Files:**
- `internal/worktree/slot.go` (новый)
- `internal/worktree/slot_test.go` (новый)
- `docs/architecture.md` — дерево пакетов

**What to do:**
1. `func SlotName(cfg config.SessionConfig) string`:
   - `cfg.WorktreeName` непустой → он (после `strings.TrimSpace`);
   - `cfg.TaskSource` вида `STATUS-P<N>.md` (регистр не важен, допускаются
     `.MD`) → `p<N>-work`;
   - иначе — имя сессии в нижнем регистре, всё кроме `a-z0-9` заменено на `-`,
     повторные `-` схлопнуты, + `-work` (`"Chat"` → `chat-work`,
     `"Developer 1"` → `developer-1-work`).
   Результат всегда проходит проверку `^[a-z0-9-]+$` (как `case "$slot"` в
   `worktree-pool.sh`).
2. `func SlotPath(projectPath, slot string) string` →
   `<projectPath>/.claude/worktrees/<slot>`.
3. `func EnsureSlot(ctx context.Context, projectPath, slot, mainBranch string, progress func(string)) (path string, created bool, err error)`:
   - если `<path>/.git` — файл с `gitdir:` на существующий каталог
     (зарегистрированный worktree) → проверить гард 3: в gitdir есть
     `index.lock`, нет `index` и `git -C <path> log --oneline <main>..HEAD`
     пуст → удалить `index.lock`, `git -C <path> reset --hard HEAD`, вызвать
     `progress("починил прерванный checkout слота …")`. Если lock есть, но
     свои коммиты тоже есть — вернуть ошибку, ничего не трогать. Иначе — вернуть
     путь, `created=false`;
   - каталог существует, но не worktree → ошибка с текстом, что это осиротевший
     остаток (`git worktree list`, `git worktree prune`, удалить вручную) —
     **ничего не удалять**;
   - каталога нет → `progress("создаю рабочую папку сессии …")`,
     `git worktree add --detach <path> <mainBranch>`, `created=true`. Ошибка git
     возвращается с его stderr;
   - перед созданием убедиться, что `.claude/worktrees/` игнорируется: если
     `git check-ignore -q .claude/worktrees/x` не срабатывает — дописать строку
     `/.claude/worktrees/` в `<git-common-dir>/info/exclude` (не в
     `.gitignore` — см. инвариант 2).
   - все вызовы git — через `exec.CommandContext` с `proc.HideConsole`, как в
     `internal/gitutil` (можно вынести общий `runGit`, если удобно).
4. Тесты на временном репозитории (`t.TempDir()`, `git init`, один коммит):
   SlotName на всех трёх ветках правила; создание слота; повторный вызов не
   пересоздаёт (`created=false`); слот на чужой ветке остаётся на ней;
   имитация прерванного checkout'а (удалить `index`, создать `index.lock`) →
   чинится; то же со своим коммитом → ошибка, файлы на месте; каталог-не-worktree
   → ошибка, каталог на месте; `info/exclude` дописывается один раз.

**Done when:** `go build ./... && go vet ./... && go test ./internal/worktree/...`
зелёные; пакет нигде ещё не вызывается (подключение — WT-02); строка пакета в
дереве `docs/architecture.md`.

---

## WT-02: запуск CLI в слоте и этапы старта в ленте

**Depends on:** WT-01

**Files:**
- `internal/session/session.go` (`runOnce` ~1472, сборка аргументов ~2485)
- `internal/session/hermes_runtime.go` (`runOnceHermes` ~133, аргументы ~434, `cmd.Dir` ~259)
- `internal/session/manager.go` (старт сессии)
- тесты: `internal/session/*_test.go` (fakeclaude, см. `docs/testing-harness.md`)
- `docs/design-decisions.md`, `config.example.toml` (комментарий к `use_worktree` / `worktree_name`)

**What to do:**
1. Убрать передачу `--worktree` (и `--worktree <name>`) из обоих runtime'ов.
2. В `runOnce`/`runOnceHermes` перед `exec.CommandContext`: если
   `s.Config.UseWorktree` —
   `slot := worktree.SlotName(s.Config)`,
   `path, created, err := worktree.EnsureSlot(ctx, s.ProjectPath, slot, gitutil.MainBranch(ctx, s.ProjectPath), s.emitManagerLog)`.
   - Перед этим `gitutil.EnsureRepoWithCommit` (как сейчас делает `app.go`
     ~814 для `--worktree`), если проект ещё не git/без коммитов.
   - Успех → `cmd.Dir = path`, в лог `session.launch` писать `cwd=<path>` и
     `slot=<slot>`. В ленту — `emitManagerLog`:
     `Рабочая папка: .claude/worktrees/<slot> (ветка <branch|detached>)`;
     если `created` — ещё и время создания.
   - Ошибка → **не падать**: `emitManagerLog("Рабочая папка не готова: <err>.
     Сессия запущена в корне проекта.")`, `cmd.Dir = s.ProjectPath`,
     `logger.L.Warn("session.slot_failed", ...)`.
3. Запрет двойного занятия: `SessionManager` при старте сессии с
   `UseWorktree` проверяет, нет ли **работающей** сессии того же проекта с тем
   же `SlotName`; если есть — старт отклоняется с ошибкой
   `слот <slot> занят сессией <name>` (видна в UI так же, как другие ошибки
   старта).
4. Этапы старта в ленте (сейчас от нажатия Start до `init` проходит до минут
   без единой строки). Через `emitManagerLog`:
   - перед сборкой промпта/primer — `Готовлю промпт…`;
   - после `cmd.Start()` — `CLI запущен (pid N), жду инициализации…`;
   - на событии `init` — `CLI готов за X с` (от `cmd.Start()`); если X < 3 с,
     строку не выводить, чтобы не шуметь;
   - если `init` нет 15 с — одна строка `CLI ещё инициализируется (15 с)…`
     и далее раз в 30 с с новым числом, пока не придёт `init` или процесс не
     завершится (таймер останавливается в обоих случаях; горутина не течёт).
5. Resume: транскрипт CLI привязан к рабочей папке. Проверить на fakeclaude
   или чтением кода crash-recovery, что `--resume` после перезапуска идёт с тем
   же `cmd.Dir` (слот), и что при первом запуске после включения галки
   (старый транскрипт был от корня проекта) ошибка «conversation not found»
   обрабатывается так же, как сейчас для устаревшей цели resume (сброс и
   свежий старт), а не роняет сессию. Если обработки нет — добавить по образцу
   `errSessionNotFound` в `session.go` ~485.
6. Документация: в `docs/design-decisions.md` — раздел «Session work folder
   (slot)» (почему не `--worktree`: замер выше, инварианты блока);
   `config.example.toml` — переписать комментарий к `use_worktree` /
   `worktree_name` (имя слота, правило по умолчанию).

**Done when:** `go build ./... && go vet ./... && go test ./...` зелёные; тест
на fakeclaude: сессия с `UseWorktree=true` в temp-репозитории запускается с
рабочей папкой `.claude/worktrees/<slot>`, второй старт переиспользует слот,
в аргументах нет `--worktree`; вторая сессия с тем же слотом не стартует; в
ленте видны строки этапов. Вручную (пользователь): Lumen S1 с включённой
галкой — первая строка в ленте в течение секунды, `init` за единицы секунд,
новых каталогов в `.claude/worktrees/` не появляется.

---

## WT-03: галка ↔ протокол, раздел в `CLAUDE.md`, умолчания

**Depends on:** WT-02

**Files:**
- `internal/analysis/protocol.go`, `internal/analysis/protocol/*.tmpl`
- `internal/session/manager.go` (`InstallProtocol` ~2606)
- `app.go` (`InstallSessionProtocol` ~590, создание P1-сессии ~560–585, новая
  сессия по умолчанию ~719), новый биндинг при необходимости
- `frontend/src/components/Settings.svelte` (галка ~1699, умолчание ~177,
  кнопка протокола ~1380), файлы i18n
- `docs/architecture.md` (таблица биндингов, если добавлен метод),
  `docs/design-decisions.md`

**What to do:**
1. Шаблоны протокола сейчас жёстко используют слот `p1-work` и префикс `p1-`
   (`cm-task-start.md.tmpl`: `worktree-pool.sh p1-work p1-$ARGUMENTS`).
   Добавить в `ProtocolParams` поля `Slot` и `BranchPrefix`, вычислять их из
   той же `worktree.SlotName` (для `STATUS-P<N>.md` → `p<N>-work` / `p<N>-`),
   подставить в шаблоны. Для текущего P1 вывод шаблонов не должен измениться
   (тест: рендер с P1-параметрами побайтно равен прежнему).
2. Раздел в `CLAUDE.md` проекта: `WriteProtocolFiles` дополнительно, если в
   `<project>/CLAUDE.md` нет подстроки `docs/git-workflow.md`, дописывает в конец
   короткий раздел (3–5 строк, по-английски, как остальной протокол):
   `## Developer sessions` — сессия с очередью работает в своём постоянном слоте
   `.claude/worktrees/p<N>-work`, задачи берёт через `/cm-task-start`,
   завершает через `/cm-task-finish`, полный протокол — `docs/git-workflow.md`.
   Нет `CLAUDE.md` — создать с этим разделом. Есть упоминание — не трогать
   (Lumen). Путь `CLAUDE.md` возвращается в списке записанных файлов.
3. UI: при включении галки «Use worktree» у сессии, если в проекте нет ни
   `scripts/worktree-pool.sh`, ни `docs/git-workflow.md` — под галкой inline-
   баннер (не `window.confirm`): «В проекте нет протокола рабочих папок. Без
   него сессия работает в слоте, но ветками задач никто не управляет.
   [Установить протокол]». Кнопка вызывает существующий `InstallSessionProtocol`
   и показывает список записанных файлов. Проверка наличия — новый биндинг
   `HasSessionProtocol(project) (bool, error)` либо поле в уже возвращаемых
   данных проекта; биндинг → строка в таблице `docs/architecture.md`.
   Подсказка (title) у галки: «Постоянная папка .claude/worktrees/<slot>; создаётся
   один раз, незаконченная задача продолжается в ней».
4. Умолчания:
   - новая сессия из Settings (`Settings.svelte` ~177, `app.go` ~719) —
     `UseWorktree: false`;
   - P1-сессия, которую создаёт менеджер вместе с протоколом (`app.go`
     ~560–585), — `UseWorktree: true`: теперь галка готовит тот же слот, что
     занимает протокол, и CLI стартует прямо в нём. Переписать комментарий там
     же (старое обоснование про анонимный worktree больше не верно).
5. `docs/design-decisions.md`: дополнить раздел из WT-02 — как галка связана с
   протоколом и почему `CLAUDE.md` дописывается только при отсутствии ссылки.

**Done when:** `go build ./... && go vet ./... && go test ./...` и фронтовые
тесты зелёные; тест `WriteProtocolFiles`: в temp-проект без `CLAUDE.md` —
файл создаётся с разделом; с `CLAUDE.md`, где уже есть `docs/git-workflow.md`, —
не меняется; с `CLAUDE.md` без ссылки — раздел дописан один раз (второй вызов
ничего не пишет); шаблоны с P1-параметрами побайтно прежние, с P3 — `p3-work`.
В UI: включение галки в проекте без протокола показывает баннер, кнопка
ставит файлы; в Lumen баннера нет.

## Status

| Задача | Статус | Ключевые файлы |
|---|---|---|
| WT-01 | ✓ DONE (2026-10-15) | internal/worktree/ |
| WT-02 | ○ TODO | internal/session/, запуск CLI в слоте, лента |
| WT-03 | ○ TODO | протокол, CLAUDE.md, умолчания |
