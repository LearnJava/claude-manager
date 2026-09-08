# Git workflow

How a developer session takes a task, finishes it, and gets its work into
`master`. Ported from lumen-browser's protocol on 2026-09-08 after the incident
described below.

## Why this exists

On 2026-09-08 the `Программист 1` session implemented **the same task (LN-01)
seven times** — four times to a green, committed, fully documented state — and
`master` received none of it. Cost: ~$12 and a day of wall-clock, delivered
value: zero.

The mechanism was not a bug in any one place, it was the absence of this
document:

1. The session ran with the manager's `use_worktree = true`, which creates an
   **unnamed worktree from HEAD on every process start** — one interrupted run
   (rate limit at 16:38, a 403 at 17:20) and the next run woke up in a clean
   tree at `master`.
2. Nothing told the session to merge, so each attempt committed inside its own
   throwaway worktree and died there.
3. The task queue (`LEARN-STATUS.md`) still listed LN-01, because the pointer
   was deleted in a branch nobody merged — so the next session dutifully took
   LN-01 again.

The fix is not "retry better". It is that **the state of a task lives in git,
not in the manager's memory**: a `p<N>-<task>` branch that exists means the task
is taken and started, and any new session — after a crash, a rate limit, an app
restart — sees it with `git branch` and continues it.

## Branches

**All work happens in feature branches. Direct commits to `master` are
forbidden** — including docs, "minor fixes" and a one-line status update.

Branch name: `p<N>-<task>`, kebab-case, developer number mandatory —
`p1-ln02-signatures`, `p2-roadmap-tree`. The prefix is what identifies the owner
of a branch left behind by a crashed session.

**The branch is the task reservation.** There is no separate "in progress"
registry: a parallel session runs `git branch -a`, sees `p1-ln02-…` and skips
that task. The pointer line in the `*-STATUS.md` queue **stays in place** until
the task is finished — it is the queue, not the progress tracker.

## Worktree pool — mandatory

Every session works in its own slot: `.claude/worktrees/p<N>-work`.

```bash
cd "$(bash scripts/worktree-pool.sh p1-work p1-<task> | tail -1)"
```

**The slot is persistent — one per developer, not one per task.** Only the
branch changes. This is the whole point: a slot and its branch survive an
interrupted session, whereas the manager's own `--worktree` flag makes a fresh
anonymous one every run and loses everything not merged. `frontend/node_modules`
also stays warm, which a `git worktree remove` would delete.

Therefore: **`use_worktree` must be `false`** in the manager's session config
for any session that follows this protocol. The protocol owns the worktree, the
manager does not.

The script refuses to switch a slot that has uncommitted work or commits not
merged into `master` — a crashed session's work is never wiped silently. Commit
(`git -C <slot> commit -am "wip: ..."`) or merge first. `worktree-pool.sh list`
shows what each slot holds.

Ad-hoc worktrees are allowed for one-off needs (a merge helper); the path must
be under `.claude/worktrees/`, and they are removed with `git worktree remove`
right after use.

## Session start

1. **`git pull origin master` first**, before reading any `*-STATUS.md` or
   creating a branch. Starting stale means acting on stale pointers.
2. **`git branch -a`.** If a `p<N>-…` branch already exists for you — that is
   your own interrupted task. **Continue it**; do not start a new one and do not
   take a different task. This replaces crash recovery: it works no matter how
   the previous run died.
3. Otherwise take the **top** pointer line of your `*-STATUS.md` whose
   dependencies are done, and occupy your slot with a new `p<N>-<task>` branch.
4. Push the branch immediately (`git push origin p<N>-<task>`) — its existence
   on the remote is what reserves the task for parallel sessions.

Take the queue strictly top-down: the order encodes dependencies, not
preference.

## Commits

- One logical step = one commit. `go build ./...` must pass before each.
- Conventional Commits subject in English (`feat:`, `fix:`, `refactor:`,
  `docs:`), imperative, body explains *why*.
- Stage specific paths (`git add path1 path2`), not `git add -A`.
- Trailer at the end naming the model that authored it:
  `Co-Authored-By: Claude <model> <noreply@anthropic.com>`

## Merge and push after every commit

**Every commit is merged into `master` and pushed right after it is made.**
Work is not accumulated on a branch until the task is done.

```bash
go build ./... && go test ./...        # local gate
git commit -m "..."
git -C <repo-root> merge --no-ff p1-<task> -m "Merge p1-<task>: ..."
git push origin master
```

`--no-ff` is required — it keeps "this commit series = one task" visible in
`git log --graph`.

**Why per commit, not per task:** unmerged work does not exist for anyone else,
and the incident above is what that costs. Frequent small merges also replace
one large end-of-task conflict with several trivial ones.

If the root checkout is dirty and blocks the merge, merge in a throwaway
worktree taken from the remote instead:

```bash
git worktree add .claude/worktrees/merge-tmp -b merge-tmp-<task> origin/master
git -C .claude/worktrees/merge-tmp merge --no-ff p1-<task> -m "Merge p1-<task>: ..."
git -C .claude/worktrees/merge-tmp push origin HEAD:master
git worktree remove .claude/worktrees/merge-tmp && git branch -D merge-tmp-<task>
```

## Task completion

**Run `/cm-task-finish`.** It is the executable protocol; do not hand-run the
steps beside it.

What must be true when a task is closed — the invariant, not the procedure:

- **The gate ran and is green:** `go build ./...`, `go vet ./...`,
  `go test ./...`, plus `npm --prefix frontend test` if the frontend changed.
- **Docs moved with the code** — the doc-sync matrix in `CLAUDE.md`; at minimum
  the task's pointer line is deleted from its `*-STATUS.md` and its row in the
  task file's Status table becomes `✓ DONE (date)`.
- **The branch is merged `--no-ff` into `master` and pushed.** Never by
  committing on `master` directly — put the status edit in the task branch and
  let the merge carry it.
- **The slot is freed, then the branch deleted** (in that order — a slot holding
  the branch makes `git branch -d` fail). The slot itself stays.
- **The remote branch is deleted** if one was pushed.

## If work is cancelled

```bash
bash scripts/worktree-pool.sh release p<N>-work   # refuses while unmerged commits exist
git branch -D p<N>-<task>
git push origin --delete p<N>-<task>
```
Then remove the task's pointer line only if the task is genuinely abandoned;
otherwise leave the queue untouched so the next session picks it up.

## Forbidden

- Any commit directly to `master`.
- Force-push, rewriting published history, `git config`, `--no-verify`.
- Merging a red gate.
- `git push` outside this protocol — see `CLAUDE.md` §Git workflow for the
  exact boundary of what an autonomous session may push.
