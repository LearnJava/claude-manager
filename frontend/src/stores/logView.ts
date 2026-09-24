// How the log stream renders message bodies: formatted markdown (default) or
// raw text. Persisted like the theme so the choice survives a restart — it is
// a reading preference, not per-session state.

import { writable } from 'svelte/store';

const STORAGE_KEY = 'cm.logMarkdown';

function readStored(): boolean | null {
    try {
        const v = localStorage.getItem(STORAGE_KEY);
        if (v === '1') return true;
        if (v === '0') return false;
    } catch (_) {
        // localStorage may be unavailable (sandboxed contexts).
    }
    return null;
}

// Default: formatted. Raw is the escape hatch, not the norm.
export const logMarkdown = writable<boolean>(readStored() ?? true);

logMarkdown.subscribe((on) => {
    try {
        localStorage.setItem(STORAGE_KEY, on ? '1' : '0');
    } catch (_) {
        /* ignore */
    }
});

export function setLogMarkdown(on: boolean) {
    logMarkdown.set(on);
}

export function toggleLogMarkdown() {
    logMarkdown.update((v) => !v);
}

// Feed vs classic log rendering (VIEW-TASKS.md UI-02). Same persistence
// pattern as logMarkdown above: a reading preference, not per-session state.
// Default 'classic' — the feed is opt-in until UI-03/04/05 round it out
// (UI-05 flips this default once the block is done).
const LAYOUT_STORAGE_KEY = 'cm.logLayout';

export type LogLayout = 'feed' | 'classic';

function readStoredLayout(): LogLayout | null {
    try {
        const v = localStorage.getItem(LAYOUT_STORAGE_KEY);
        if (v === 'feed' || v === 'classic') return v;
    } catch (_) {
        // localStorage may be unavailable (sandboxed contexts).
    }
    return null;
}

export const logLayout = writable<LogLayout>(readStoredLayout() ?? 'classic');

logLayout.subscribe((v) => {
    try {
        localStorage.setItem(LAYOUT_STORAGE_KEY, v);
    } catch (_) {
        /* ignore */
    }
});

export function setLogLayout(v: LogLayout) {
    logLayout.set(v);
}

export function toggleLogLayout() {
    logLayout.update((v) => (v === 'feed' ? 'classic' : 'feed'));
}
