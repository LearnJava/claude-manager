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
