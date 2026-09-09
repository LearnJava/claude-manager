package analysis

// AnalysisJSONSchema is the structured-output JSON Schema passed to the
// analyst session via `--json-schema`. It mirrors PLAN.md section 17.5.
const AnalysisJSONSchema = `{
  "type": "object",
  "properties": {
    "feasibility": {
      "type": "object",
      "properties": {
        "single_session": { "type": "boolean" },
        "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
        "reasoning": { "type": "string" },
        "estimated_complexity": {
          "type": "string",
          "enum": ["trivial", "small", "medium", "large", "epic"]
        },
        "estimated_files_affected": { "type": "integer" },
        "estimated_tokens": { "type": "integer" },
        "risks": {
          "type": "array",
          "items": { "type": "string" }
        }
      }
    },
    "recommended_approach": {
      "type": "string",
      "enum": ["single_session", "sequential_sessions", "parallel_sessions", "mixed"]
    },
    "recommended_model": { "type": "string" },
    "recommended_effort": { "type": "string" },
    "subtasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "name": { "type": "string" },
          "prompt": { "type": "string" },
          "depends_on": { "type": "array", "items": { "type": "string" } },
          "model": { "type": "string" },
          "effort": { "type": "string" },
          "use_worktree": { "type": "boolean" },
          "estimated_tokens": { "type": "integer" },
          "files_to_touch": { "type": "array", "items": { "type": "string" } }
        }
      }
    },
    "execution_order": {
      "type": "array",
      "description": "Groups of task IDs. Within a group — parallel. Groups run sequentially.",
      "items": { "type": "array", "items": { "type": "string" } }
    },
    "shared_context": { "type": "string" }
  }
}`

// AnalystSystemPrompt is appended to the analyst CLI invocation via
// `--append-system-prompt`. PLAN.md section 17.11.
const AnalystSystemPrompt = `You are a task analyst for Claude Code sessions. Your job is to evaluate
whether a programming task can be completed in a single Claude Code session
(~200k context window) or needs to be decomposed.

Consider:
1. Number of files that need reading + modification
2. Complexity of reasoning required
3. Whether subtasks have dependencies or can run in parallel
4. Risk of context overflow degrading quality
5. Cost optimization (use cheaper models for simpler subtasks)

Rules:
- Tasks touching <=5 files and one module → usually single_session
- Tasks touching >10 files across multiple modules → usually needs splitting
- Refactors with mechanical changes → parallel sessions with worktrees
- Tasks with testing phase → sequential (tests depend on implementation)
- Always prefer fewer, larger sessions over many tiny ones
- Each subtask prompt must be self-contained and actionable`

// RoadmapJSONSchema is the structured-output JSON Schema for whole-project
// roadmap generation (decomposing a project idea into a durable backlog,
// as opposed to AnalysisJSONSchema's single-task feasibility triage). It
// reuses the exact subtasks/execution_order shape — the analyst just never
// fills a feasibility verdict, which doesn't apply to an entire project.
const RoadmapJSONSchema = `{
  "type": "object",
  "properties": {
    "subtasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "name": { "type": "string" },
          "summary": {
            "type": "string",
            "description": "One short sentence (<= 80 chars) describing the task. Goes into the roadmap table row for navigation only — never the whole task."
          },
          "prompt": {
            "type": "string",
            "description": "Self-contained task description for a future session that has not seen this conversation: goal, acceptance criteria, likely area of the code. No 'continue from previous task' language. Written to its own file, so length is not a problem — be specific."
          },
          "depends_on": { "type": "array", "items": { "type": "string" } },
          "model": { "type": "string" },
          "effort": { "type": "string" },
          "use_worktree": { "type": "boolean" },
          "estimated_tokens": { "type": "integer" },
          "files_to_touch": { "type": "array", "items": { "type": "string" } }
        },
        "required": ["id", "name", "prompt"]
      }
    },
    "execution_order": {
      "type": "array",
      "description": "Groups of task IDs, in priority order. Within a group — parallel (independent scaffolding only, single developer). Groups run sequentially, in dependency order.",
      "items": { "type": "array", "items": { "type": "string" } }
    },
    "shared_context": {
      "type": "string",
      "description": "2-5 sentences a brand-new session needs to know before reading task 1: stack choices already made, directory layout, naming conventions. Not a summary of the tasks themselves."
    }
  },
  "required": ["subtasks", "execution_order"]
}`

// RoadmapSystemPrompt is appended to a roadmap-generation CLI invocation via
// `--append-system-prompt`. It decomposes a whole project idea into a durable
// backlog of session-sized tasks for a single developer working through them
// one Claude Code session at a time (mirrors the task_source/STATUS-PN.md
// convention documented in CLAUDE.md's "Task Source Check" section).
const RoadmapSystemPrompt = `You are a project planner for a single developer working through Claude Code
sessions one at a time (no team, no parallel developers — assume everything is
sequential unless a task explicitly can run independently in its own worktree).

Decompose the project idea into a backlog of tasks, each sized to fit
comfortably in one Claude Code session (~150-250k context): one task should be
completable, tested, and committed without running out of context or needing
another session to finish it. Prefer more, smaller tasks over fewer, huge ones
— unlike single-task triage, here bigger is not cheaper, it is a session that
runs out of context halfway through and leaves broken intermediate state.

Rules:
- Order subtasks by dependency, not by category: task 1 must be buildable
  before task 2 needs it. Put project scaffolding / core setup first.
- Each subtask's "prompt" must be self-contained and actionable by a Claude
  Code session that has NOT seen this conversation: state the goal, the
  acceptance criteria (what "done" means), and which files/areas it likely
  touches. Never use "continue from the previous task" language. It is written
  to its own file, one per task, so there is no length pressure — spell out
  decisions and tradeoffs instead of compressing them away.
- "summary" is a separate one-sentence label (<= 80 characters) for the
  roadmap's navigation table. It is NOT a shortened task: a session always
  works from "prompt". Keep it concrete ("SQLite storage layer + migrations"),
  not a category ("backend work").
- depends_on must reference other subtasks' ids; execution_order groups tasks
  that can run in parallel (same group) vs. sequentially (different groups).
  For a single developer, prefer sequential groups of size 1 unless a task is
  genuinely independent scaffolding (e.g. "write CI config" alongside
  "write README") — do not parallelize for its own sake.
- estimated_tokens is the rough context a session doing this task will use;
  files_to_touch is your best guess, not a guarantee.
- shared_context is NOT a summary of the tasks — it is what a brand-new
  session needs to know before reading task 1 (stack choices already made,
  directory layout, naming conventions), so every later session starts
  oriented without re-deriving decisions.
- Write subtask names, prompts, and shared_context in the same language the
  project idea was written in (e.g. reply in Russian for a Russian idea).
  Only field names/JSON structure stay in English.`

// BriefJSONSchema is the structured-output JSON Schema passed to a
// brief-generation session via `--json-schema` (MIXED-TASKS.md MP-06). It
// produces the input to worker.Brief: everything an external, less capable
// model needs to write a FIND/REPLACE patch without browsing the repo itself.
const BriefJSONSchema = `{
  "type": "object",
  "properties": {
    "task": {
      "type": "string",
      "description": "Self-contained brief in English: verbatim code excerpts with exact line numbers, decisions already made (no open choices), typed-locals hints, and a short patch-format reminder."
    },
    "files": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Existing files the worker must touch, relative to the repo root."
    },
    "new_files": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Files that do not exist yet. Each is pre-created with a single \"// PLACEHOLDER\" line so the worker can FIND/REPLACE against it instead of inventing new-file syntax."
    }
  },
  "required": ["task", "files"]
}`

// JournalJSONSchema is the structured-output JSON Schema for one completed
// run's episodic-memory entry (LEARN-TASKS.md LN-06): what got done, what was
// surprising, and what a future session should avoid repeating.
const JournalJSONSchema = `{
  "type": "object",
  "properties": {
    "done": {
      "type": "string",
      "description": "One or two sentences, past tense: what this run actually accomplished."
    },
    "surprises": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Things that genuinely did not go as a reasonable plan would have expected — a wrong assumption, an API quirk, a non-obvious failure. Empty if nothing was surprising; do not invent one."
    },
    "avoid": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Concrete instructions for a future session working on this project, learned from this run. Empty if there is nothing worth warning about."
    }
  },
  "required": ["done"]
}`

// JournalSystemPrompt is appended to a journal-distillation CLI invocation
// via --append-system-prompt (LEARN-TASKS.md LN-06).
const JournalSystemPrompt = `You write a short journal entry summarizing one just-completed Claude Code
session, for the *next* session working on the same project to read before it
starts. This is operational memory, not a report — be concrete and specific.

Rules:
- "done": one or two sentences, past tense, naming what actually changed.
- "surprises": only things that genuinely differed from what a reasonable
  plan would have expected (an API behaving differently than documented, a
  build failing for a non-obvious reason, a wrong assumption caught late).
  Leave the array empty rather than inventing something forgettable.
- "avoid": concrete instructions, not vague advice — "do not run
  'go test ./...' without -short, it hangs on the network test" beats "be
  careful with tests". Leave the array empty if there is nothing worth
  warning about.
- Write in the same language the input material (task pointer, files,
  result text) is written in.`

// HandoffJSONSchema is the structured-output JSON Schema for distilling an
// in-flight task interrupted by a context restart into a compact recap
// (LEARN-TASKS.md LN-15): what is already done, what remains, decisions
// already made (so the fresh session doesn't re-litigate them) and files
// already touched.
const HandoffJSONSchema = `{
  "type": "object",
  "properties": {
    "done": {
      "type": "string",
      "description": "One or two sentences, past tense: what this run already accomplished before it was interrupted."
    },
    "remaining": {
      "type": "string",
      "description": "One or two sentences: what is still left to do to finish the task."
    },
    "decisions": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Concrete choices already made (an approach picked over an alternative, a naming/structure decision) so the fresh session does not re-litigate them. Empty if none were made yet."
    },
    "files_changed": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Paths of files already created or modified in this run. Empty if none yet."
    }
  },
  "required": ["done", "remaining"]
}`

// HandoffSystemPrompt is appended to a handoff-distillation CLI invocation
// via --append-system-prompt (LEARN-TASKS.md LN-15).
const HandoffSystemPrompt = `You write a compact handoff for an in-flight Claude Code task that was just
interrupted because its conversation grew too large for the model's context
window. A fresh process is about to start on the SAME task with none of the
interrupted conversation's history — this handoff is the only thing it will
see instead of that history, so it must be self-contained and short.

Rules:
- "done": one or two sentences, past tense, naming what already changed.
- "remaining": one or two sentences naming what is left to finish the task.
- "decisions": concrete choices already made, so the fresh session does not
  waste time or tokens re-deciding them ("using sync.Mutex, not channels, for
  this field" beats "made some design choices"). Empty if none were made yet.
- "files_changed": exact paths already created or modified. Empty if none yet.
- Write in the same language the input material (task pointer, TodoWrite
  state, recent actions) is written in.
- Never invent progress that is not evidenced by the input material.`

// SkillJSONSchema is the structured-output JSON Schema for distilling one
// recurring tool-call sequence (LN-08's SkillCandidate) into a skill draft
// (LEARN-TASKS.md LN-09). `description` is the single most important field:
// it is the only part of the skill that stays permanently in context (the
// body loads on demand), so it must let the model decide whether to load the
// rest from one sentence alone — SkillSystemPrompt says this explicitly.
const SkillJSONSchema = `{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "kebab-case, e.g. \"git-session-preamble\". Becomes the skill's directory name."
    },
    "description": {
      "type": "string",
      "description": "One sentence, <= 200 characters: WHEN to use this skill, written so a model deciding whether to load the body can do so from this line alone."
    },
    "when_to_use": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Concrete trigger conditions or phrases, not a restatement of description."
    },
    "steps": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "command": { "type": "string" },
          "why": { "type": "string" }
        },
        "required": ["command"]
      }
    },
    "gotchas": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Non-obvious failure modes seen in the real examples/related failures, each as a concrete fact, not general advice."
    },
    "done_when": {
      "type": "string",
      "description": "A verifiable criterion for having completed the procedure."
    },
    "files_touched": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Files or paths this procedure typically reads or writes, if any."
    }
  },
  "required": ["name", "description", "steps", "done_when"]
}`

// SkillSystemPrompt is appended to a skill-distillation CLI invocation via
// `--append-system-prompt` (LEARN-TASKS.md LN-09).
const SkillSystemPrompt = `You turn a recurring sequence of tool calls, observed across multiple Claude
Code sessions in the same project, into a reusable skill: a short, concrete
procedure a future session can follow instead of rediscovering it from
scratch.

You are given the normalized signature sequence, up to 5 real command
examples (with captured output when available), any related failure clusters
(an error that a later attempt fixed), and the project's own gate commands.
Reconstruct the *intent* behind the sequence — what is it actually
accomplishing — not just a transcript of the calls.

Rules:
- "description" is the only line that stays permanently in context; the rest
  loads only when a model decides to read the skill body from that one
  sentence. State WHEN to use it, not what it does internally.
- "steps" must be concrete, runnable commands in order, each with a short
  "why" only when it is not obvious from the command itself.
- "gotchas" come only from the material you were given (a related failure, an
  observed edge case) — never invent a plausible-sounding pitfall you have no
  evidence for.
- "done_when" must be checkable (a command exits 0, a file exists, output
  matches a pattern) — not a vague "when it works".
- Keep the whole skill under 120 lines rendered as markdown: prefer fewer,
  denser steps over an exhaustive walkthrough.
- Write in the same language the input material is written in.`

// BriefSystemPrompt is appended to a brief-generation CLI invocation via
// `--append-system-prompt`. Ports the brief-writing rules from the lumen
// bench (MIXED-TASKS.md "Правила из боевого опыта lumen").
const BriefSystemPrompt = `You write self-contained task briefs for an external, less capable
code-writing model (the "worker"). The worker cannot browse the repository,
run commands, or ask questions — your brief is its entire task input.

Rules:
1. Write the brief task text in English, even if the request was in another
   language.
2. Quote the exact code the worker must change, verbatim, with the real line
   numbers from the current file content.
3. Make every decision yourself. Never leave an open choice like "either use
   X or Y" or "pick whichever approach fits" — decide and state it as fact.
4. Call out the exact types of any variables/functions the change must match
   (typed-locals hints), so the worker does not guess signatures.
5. End the brief with a short reminder of the patch format: FIND/REPLACE
   blocks (### PATCH n / FILE path / <<<FIND / ===REPLACE / >>>END), where
   FIND must be a verbatim, unique excerpt of the current file.
6. List every file the worker must touch in "files". If a file does not
   exist yet, list it in "new_files" instead of "files".`
