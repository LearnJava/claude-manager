// Experience layer (LEARN-TASKS.md LN-03..): thin wrappers around the
// GetTopActions/GetActionSamples Wails bindings plus the row shapes they
// return, so the "Actions" tab (and the tabs later LN items add to the same
// ExperiencePanel modal) share one fetch/type surface instead of each
// hand-rolling it.

import { GetActionSamples, GetTopActions } from '../../wailsjs/go/main/App';

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
