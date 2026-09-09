// Experience layer (LEARN-TASKS.md LN-03..): thin wrappers around the
// GetTopActions/GetActionSamples Wails bindings plus the row shapes they
// return, so the "Actions" tab (and the tabs later LN items add to the same
// ExperiencePanel modal) share one fetch/type surface instead of each
// hand-rolling it.

import {
    AddPermissionRule,
    ApproveSkill,
    ArchiveSkill,
    GetActionSamples,
    GetDurationProfile,
    GetPermissionCandidates,
    GetSkills,
    GetTopActions,
} from '../../wailsjs/go/main/App';

// The Go store.SignatureStat/ActionRow structs are exposed without JSON
// tags, so field names arrive capitalized — same convention as
// store.SessionRun already used by History.svelte/CostDashboard.svelte.
export interface SignatureStat {
    Sig: string;
    Tool: string;
    Count: number;
    DistinctRuns: number;
    ErrorRate: number;
    SumOutTokens: number;
    SampleArgs: string[] | null;
    FirstSeen: string;
    LastSeen: string;
}

export interface ActionRow {
    ID: number;
    Project: string;
    Session: string;
    RunID: number | null;
    CLISessionID: string;
    TaskPtr: string;
    StepIndex: number;
    Tool: string;
    Sig: string;
    Arg: string;
    IsError: boolean;
    OutTokens: number;
    ResultChars: number;
    Timestamp: string;
}

// fetchTopActions aggregates action_signatures for a project over the last
// `days` days, most frequent signature first (store.TopSignatures).
export async function fetchTopActions(project: string, days: number): Promise<SignatureStat[]> {
    const raw = (await GetTopActions(project, days)) as SignatureStat[] | null;
    return raw ?? [];
}

// fetchActionSamples returns concrete example rows for one (project, sig)
// pair — the click-through from an aggregated signature to what it covers.
export async function fetchActionSamples(
    project: string,
    sig: string,
    limit = 20,
): Promise<ActionRow[]> {
    const raw = (await GetActionSamples(project, sig, limit)) as ActionRow[] | null;
    return raw ?? [];
}

// SignatureDuration mirrors experience.SignatureDuration — the "Timing" tab's
// row shape (LEARN-TASKS.md LN-18): how long a normalized command actually
// takes in this project, aggregated from action_signatures.dur_sec.
export interface SignatureDuration {
    Sig: string;
    Tool: string;
    Count: number;
    MedianSec: number;
    P90Sec: number;
    MaxSec: number;
    TotalSec: number;
    FailRate: number;
}

// fetchDurationProfile aggregates action_signatures durations for a project,
// most time-consuming (by median) first.
export async function fetchDurationProfile(project: string): Promise<SignatureDuration[]> {
    const raw = (await GetDurationProfile(project)) as SignatureDuration[] | null;
    return raw ?? [];
}

// PermissionCandidate is a suggested permission rule mined from repeated
// waiting_permission events (LEARN-TASKS.md LN-04) — a (tool, pattern) that
// keeps asking and has been consistently approved. Safe means it cleared the
// backend's read-only whitelist classifier and is offered an "Add rule"
// button; everything else is listed under "needs manual review" only.
export interface PermissionCandidate {
    Tool: string;
    Pattern: string;
    Count: number;
    AllowCount: number;
    DenyCount: number;
    Safe: boolean;
    FirstSeen: string;
    LastSeen: string;
}

export interface CandidateSet {
    Safe: PermissionCandidate[];
    NeedsReview: PermissionCandidate[];
}

// fetchPermissionCandidates aggregates permission_events for a project over
// the last `days` days into suggested auto-allow rules.
export async function fetchPermissionCandidates(
    project: string,
    days: number,
): Promise<CandidateSet> {
    const raw = (await GetPermissionCandidates(project, days)) as CandidateSet | null;
    return raw ?? { Safe: [], NeedsReview: [] };
}

// addPermissionRule appends {tool, pattern, decision} to one session's
// PermissionRules via the same GetConfig/UpdateConfig round-trip every other
// config edit uses — the "Add rule" button's action.
export async function addPermissionRule(
    project: string,
    session: string,
    tool: string,
    pattern: string,
    decision: string,
): Promise<void> {
    await AddPermissionRule(project, session, tool, pattern, decision);
}

// Skill mirrors store.Skill — one distilled procedure, draft through
// approved/archived (LEARN-TASKS.md LN-09/10/11). Same PascalCase-from-Go
// convention as SignatureStat/ActionRow above. ApprovedAt/ArchivedAt are Go
// *time.Time — null until the corresponding transition happens, so Status is
// still the cheaper thing to branch on, but these are here for display
// ("Approved 2026-09-09").
export interface Skill {
    ID: number;
    Project: string;
    Name: string;
    Status: string; // draft | approved | archived
    DraftJSON: string;
    MD: string;
    SourceJSON: string;
    CreatedAt: string;
    ApprovedAt: string | null;
    ArchivedAt: string | null;
}

// SkillDraftStep/SkillDraft mirror analysis.SkillStep/SkillDraft — decoded
// from Skill.DraftJSON for display (description, when-to-use) without a
// second round-trip through the CLI.
export interface SkillDraftStep {
    command: string;
    why?: string;
}

export interface SkillDraft {
    name: string;
    description: string;
    when_to_use?: string[];
    steps: SkillDraftStep[];
    gotchas?: string[];
    done_when: string;
    files_touched?: string[];
}

// parseSkillDraft decodes Skill.DraftJSON, returning null on malformed JSON
// rather than throwing — a display helper must never crash the Skills tab
// over one bad row.
export function parseSkillDraft(draftJSON: string): SkillDraft | null {
    try {
        return JSON.parse(draftJSON) as SkillDraft;
    } catch {
        return null;
    }
}

// fetchSkills lists every skill row for a project, newest first — the
// "Skills" tab's source of truth (LEARN-TASKS.md LN-10).
export async function fetchSkills(project: string): Promise<Skill[]> {
    const raw = (await GetSkills(project)) as Skill[] | null;
    return raw ?? [];
}

// approveSkill writes md to <project>/.claude/skills/<name>/SKILL.md and
// marks the row approved, returning the written path. Throws with a message
// matching /already exists/i (experience.ErrSkillFileExists) when the file
// is already there and overwrite is false — the caller's cue for the inline
// "already exists — overwrite?" banner (same convention as
// PlanReview.svelte's onWriteRoadmap/ErrRoadmapFilesExist).
export async function approveSkill(id: number, md: string, overwrite: boolean): Promise<string> {
    return await ApproveSkill(id, md, overwrite);
}

// archiveSkill marks a skill row archived — never touches any file already
// written into the project.
export async function archiveSkill(id: number): Promise<void> {
    await ArchiveSkill(id);
}
