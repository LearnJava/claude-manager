---
name: cm-task-finish
description: >
  Завершает задачу по протоколу claude-manager: go build/vet/test (+ фронтовые
  тесты, если менялся фронтенд), синхронизация документации по матрице doc-sync,
  удаление указателя из очереди, merge --no-ff в master, push, освобождение
  слота и удаление ветки. Используй когда задача реализована.
when_to_use: >
  Фразы-триггеры: "заверши задачу", "смерджи ветку", "влей ветку", "задача
  готова", "ready to merge", "закончил задачу". Также когда все тесты проходят
  и реализация завершена.
model: claude-sonnet-5
allowed-tools: Bash(git *) Bash(go *) Bash(npm *) Bash(bash scripts/*) Read Edit
---

# Завершение задачи — протокол merge в claude-manager

$ARGUMENTS — имя ветки задачи. Если не передан — `git branch --show-current`.

Полный протокол и инвариант завершения —
[docs/git-workflow.md](../../../docs/git-workflow.md).

> **Этот скилл — финальный гейт.** Не гоняй `go test ./...` вручную прямо перед
> его вызовом: по ходу работы достаточно `go build ./...`, полную проверку
> скилл делает один раз ниже.

> **Гейт гони СИНХРОННО** — обычный вызов Bash с `timeout: 600000`, НЕ
> `run_in_background`. Фоновый вывод буферизуется, выглядит пустым и провоцирует
> минуты поллинга и повторный прогон той же команды. Вывод пиши в файл и
> фильтруй grep-ом по файлу — никогда не перезапускай гейт ради другого фильтра.

> **Правило «merge+push после каждого коммита» означает, что шаги 1–5 уже
> прогонялись по ходу задачи.** Здесь они закрывают хвост последнего коммита.

## Шаг 1 — Гейт

```bash
mkdir -p .tmp
go build ./... > .tmp/gate.log 2>&1 && go vet ./... >> .tmp/gate.log 2>&1 && \
  go test ./... >> .tmp/gate.log 2>&1; echo "exit=$?"
tail -20 .tmp/gate.log          # упавшие тесты: grep -B2 "FAIL\|panic" .tmp/gate.log
```

Фронтовые тесты — **только если менялся `frontend/`**
(`git diff --name-only master...HEAD | grep ^frontend/`):

```bash
npm --prefix frontend test >> .tmp/gate.log 2>&1; echo "exit=$?"
```

Красный гейт — не завершаем. Чини, не обходи, `#nosec`/`t.Skip` без причины не
добавляй.

## Шаг 2 — Синхронизируй документацию (матрица doc-sync)

Матрица — раздел «Doc-sync update matrix» в
[CLAUDE.md](../../../CLAUDE.md). Что нужно почти всегда:

1. **Очередь `*-STATUS.md`** — удали строку-указатель завершённой задачи.
   Указатель — это НОМЕР СТРОКИ: сверяй по файлу (`sed -n 'NNp' <файл>`), а не
   по памяти, чужие правки его уводят.
2. **Таблица Status в мастер-файле задач** (`LEARN-TASKS.md`, `TASKS.md`,
   `MIXED-TASKS.md`, `HARNESS-TASKS.md`) — `● IN PROGRESS` → `✓ DONE (дата)`.
3. **Новый метод `App`** → строка в таблице «Wails Bindings (app.go)» в CLAUDE.md.
4. **Новое поле конфига** → `config.example.toml` + CLAUDE.md.
5. **Новый пакет или заметный файл** → дерево архитектуры в начале CLAUDE.md.
6. **Новый сценарий fakeclaude/fakeworker** → `testdata/scenarios/scenarios_doc.md`.
7. **Новый инструмент cm-mcp** → таблица «cm-mcp tools available to Claude».

Если сдвигал строки в мастер-файле задач — переиндексируй указатели в очереди
(вставил K строк выше строки L → все `NN ≥ L` сдвигаются на +K) и проверь
`sed -n 'NNp'`.

## Шаг 3 — Коммит документации

Если доки не вошли в коммит с кодом — отдельным коммитом:

```bash
git add <конкретные пути> && git commit -m "docs: обнови статус задачи <имя>

Co-Authored-By: Claude <модель> <noreply@anthropic.com>"
```

## Шаг 4 — Merge в master

Прямой коммит в master запрещён — правка статуса едет в ветке и приезжает
merge-коммитом.

```bash
# Вариант А — корень чист:
git -C /d/GolangProjects/claude-manager merge --no-ff $ARGUMENTS \
    -m "Merge $ARGUMENTS: <однострочное описание>"

# Вариант Б — корень держит чужие незакоммиченные файлы и блокирует merge:
git worktree add .claude/worktrees/merge-tmp -b merge-tmp-$ARGUMENTS origin/master
git -C .claude/worktrees/merge-tmp status --short        # ОБЯЗАТЕЛЬНО: пусто
git -C .claude/worktrees/merge-tmp merge --no-ff $ARGUMENTS \
    -m "Merge $ARGUMENTS: <однострочное описание>"
git -C .claude/worktrees/merge-tmp push origin HEAD:master
git worktree remove .claude/worktrees/merge-tmp && git branch -D merge-tmp-$ARGUMENTS
```

`--no-ff` обязателен.

## Шаг 5 — Push

```bash
git push origin master
```

## Шаг 6 — Освободи слот и удали ветку

Порядок важен: пока слот держит ветку, `git branch -d` отказывает.

```bash
bash scripts/worktree-pool.sh release p<N>-work
git branch -d $ARGUMENTS
git push origin --delete $ARGUMENTS     # если ветка публиковалась
```

Слот **не удаляем** — он постоянный.

## Шаг 7 — Проверь результат

```bash
git log --oneline --graph -5
git status --short
bash scripts/worktree-pool.sh list
```

Merge-коммит виден, рабочее дерево чистое, слот на detached HEAD. Сообщи
пользователю: что смержено, состояние гейта, какой указатель снят с очереди.
