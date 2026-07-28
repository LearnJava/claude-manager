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
