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
